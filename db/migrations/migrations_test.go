package migrations

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/pressly/goose/v3"
)

// dollarQuotePattern matches a PostgreSQL dollar-quote delimiter such as $$ or
// $body$.
var dollarQuotePattern = regexp.MustCompile(`\$[A-Za-z_]*\$`)

// TestDollarQuotedBodiesUseStatementMarkers guards against goose splitting a
// function body at the semicolons inside it. goose ends a statement at every
// line ending in ";" unless the statement sits between StatementBegin and
// StatementEnd markers, and it does not understand dollar quoting.
func TestDollarQuotedBodiesUseStatementMarkers(t *testing.T) {
	files, err := fs.Glob(Migrations, "*.sql")
	if err != nil {
		t.Fatalf("glob migrations: %v", err)
	}
	if len(files) == 0 {
		t.Fatal("no embedded migrations found")
	}

	for _, name := range files {
		content, err := fs.ReadFile(Migrations, name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}

		inStatement := false
		beganAt := 0
		scanner := bufio.NewScanner(strings.NewReader(string(content)))
		for lineNo := 1; scanner.Scan(); lineNo++ {
			line := strings.TrimSpace(scanner.Text())
			switch line {
			case "-- +goose StatementBegin":
				if inStatement {
					t.Errorf("%s:%d: StatementBegin inside the block opened at line %d", name, lineNo, beganAt)
				}
				inStatement, beganAt = true, lineNo
				continue
			case "-- +goose StatementEnd":
				if !inStatement {
					t.Errorf("%s:%d: StatementEnd without StatementBegin", name, lineNo)
				}
				inStatement = false
				continue
			}

			code, _, _ := strings.Cut(line, "--")
			if !inStatement && dollarQuotePattern.MatchString(code) {
				t.Errorf("%s:%d: dollar-quoted body outside -- +goose StatementBegin/StatementEnd", name, lineNo)
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatalf("scan %s: %v", name, err)
		}
		if inStatement {
			t.Errorf("%s:%d: StatementBegin is never closed", name, beganAt)
		}
	}
}

// TestMigrationsRoundTrip applies every migration to an empty schema, rolls all
// of them back, and applies them again. This is what `migrate up` on a fresh
// database and `migrate reset` followed by `migrate up` do.
func TestMigrationsRoundTrip(t *testing.T) {
	db, schema := openSchemaTestDB(t)

	applyMigrations(t, db)
	migrateDownTo(t, db, 0)

	if leftovers := schemaObjects(t, db, schema); len(leftovers) > 0 {
		t.Fatalf("objects left after migrating down to 0: %v", leftovers)
	}

	applyMigrations(t, db)
}

// schemaObjects lists the relations and functions in schema that a down
// migration should have removed. goose's own version table and objects that
// belong to an extension are not included.
func schemaObjects(t *testing.T, db *sql.DB, schema string) []string {
	t.Helper()

	const query = `
		SELECT 'relation ' || c.relname
		FROM pg_class c
		JOIN pg_namespace n ON n.oid = c.relnamespace
		WHERE n.nspname = $1
		  AND c.relkind IN ('r', 'p', 'S', 'v', 'm')
		  AND c.relname NOT IN ('goose_db_version', 'goose_db_version_id_seq')
		  AND NOT EXISTS (
			SELECT 1 FROM pg_depend d
			WHERE d.classid = 'pg_class'::regclass AND d.objid = c.oid AND d.deptype = 'e'
		  )
		UNION ALL
		SELECT 'function ' || p.proname
		FROM pg_proc p
		JOIN pg_namespace n ON n.oid = p.pronamespace
		WHERE n.nspname = $1
		  AND NOT EXISTS (
			SELECT 1 FROM pg_depend d
			WHERE d.classid = 'pg_proc'::regclass AND d.objid = p.oid AND d.deptype = 'e'
		  )`

	rows, err := db.QueryContext(context.Background(), query, schema)
	if err != nil {
		t.Fatalf("list schema objects: %v", err)
	}
	defer rows.Close()

	var objects []string
	for rows.Next() {
		var object string
		if err := rows.Scan(&object); err != nil {
			t.Fatalf("scan schema object: %v", err)
		}
		objects = append(objects, object)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate schema objects: %v", err)
	}
	slices.Sort(objects)
	return objects
}

// TestPollCachedTalliesMigration checks that migration 038 converts existing
// polls.cached_tallies values from jsonb to bigint[] and back.
func TestPollCachedTalliesMigration(t *testing.T) {
	const (
		before = 36 // last migration before 038
		target = 38
	)

	db, _ := openSchemaTestDB(t)
	ctx := context.Background()
	migrateUpTo(t, db, before)

	cases := []struct {
		name       string
		jsonb      string // stored before 038; empty means the column default
		wantArray  string // bigint[] after 038, in PostgreSQL array syntax
		wantJSONB  string // jsonb after rolling 038 back
		insertedID int64
	}{
		{name: "column default", jsonb: "", wantArray: "{}", wantJSONB: "[]"},
		{name: "empty object", jsonb: `{}`, wantArray: "{}", wantJSONB: "[]"},
		{name: "empty array", jsonb: `[]`, wantArray: "{}", wantJSONB: "[]"},
		{name: "tallies", jsonb: `[3, 0, 12]`, wantArray: "{3,0,12}", wantJSONB: "[3, 0, 12]"},
		{name: "string tallies", jsonb: `["7", "1"]`, wantArray: "{7,1}", wantJSONB: "[7, 1]"},
		{name: "null tally", jsonb: `[2, null, 4]`, wantArray: "{2,0,4}", wantJSONB: "[2, 0, 4]"},
		{name: "json null", jsonb: `null`, wantArray: "{}", wantJSONB: "[]"},
		{name: "large tally", jsonb: `[9007199254740993]`, wantArray: "{9007199254740993}", wantJSONB: "[9007199254740993]"},
	}

	for i := range cases {
		var err error
		if cases[i].jsonb == "" {
			err = db.QueryRowContext(ctx,
				`INSERT INTO polls (expires_at) VALUES (now()) RETURNING id`,
			).Scan(&cases[i].insertedID)
		} else {
			err = db.QueryRowContext(ctx,
				`INSERT INTO polls (expires_at, cached_tallies) VALUES (now(), $1::jsonb) RETURNING id`,
				cases[i].jsonb,
			).Scan(&cases[i].insertedID)
		}
		if err != nil {
			t.Fatalf("insert poll %q: %v", cases[i].name, err)
		}
	}

	migrateUpTo(t, db, target)

	for _, tc := range cases {
		var got string
		if err := db.QueryRowContext(ctx,
			`SELECT cached_tallies::text FROM polls WHERE id = $1`, tc.insertedID,
		).Scan(&got); err != nil {
			t.Fatalf("read poll %q after up: %v", tc.name, err)
		}
		if got != tc.wantArray {
			t.Errorf("%s: cached_tallies after up = %s, want %s", tc.name, got, tc.wantArray)
		}
	}

	var columnDefault string
	if err := db.QueryRowContext(ctx,
		`INSERT INTO polls (expires_at) VALUES (now()) RETURNING cached_tallies::text`,
	).Scan(&columnDefault); err != nil {
		t.Fatalf("insert poll with default tallies after up: %v", err)
	}
	if columnDefault != "{}" {
		t.Errorf("cached_tallies default after up = %s, want {}", columnDefault)
	}

	var helpers int
	if err := db.QueryRowContext(ctx,
		`SELECT count(*) FROM pg_proc WHERE proname = 'gotodon_jsonb_to_bigint_array' AND pronamespace = current_schema()::regnamespace`,
	).Scan(&helpers); err != nil {
		t.Fatalf("look up conversion helper: %v", err)
	}
	if helpers != 0 {
		t.Errorf("conversion helper still exists after up")
	}

	migrateDownTo(t, db, before)

	for _, tc := range cases {
		var raw string
		if err := db.QueryRowContext(ctx,
			`SELECT cached_tallies::text FROM polls WHERE id = $1`, tc.insertedID,
		).Scan(&raw); err != nil {
			t.Fatalf("read poll %q after down: %v", tc.name, err)
		}
		if !jsonEqual(t, raw, tc.wantJSONB) {
			t.Errorf("%s: cached_tallies after down = %s, want %s", tc.name, raw, tc.wantJSONB)
		}
	}

	if err := db.QueryRowContext(ctx,
		`INSERT INTO polls (expires_at) VALUES (now()) RETURNING cached_tallies::text`,
	).Scan(&columnDefault); err != nil {
		t.Fatalf("insert poll with default tallies after down: %v", err)
	}
	if !jsonEqual(t, columnDefault, `{}`) {
		t.Errorf("cached_tallies default after down = %s, want {}", columnDefault)
	}
}

func jsonEqual(t *testing.T, a, b string) bool {
	t.Helper()

	var left, right any
	decode := func(input string, target *any) {
		decoder := json.NewDecoder(strings.NewReader(input))
		decoder.UseNumber()
		if err := decoder.Decode(target); err != nil {
			t.Fatalf("decode JSON %q: %v", input, err)
		}
	}
	decode(a, &left)
	decode(b, &right)
	return fmt.Sprint(left) == fmt.Sprint(right)
}

func migrateUpTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()

	configureGoose(t)
	if err := goose.UpTo(db, ".", version); err != nil {
		t.Fatalf("migrate up to %d: %v", version, err)
	}
}

func migrateDownTo(t *testing.T, db *sql.DB, version int64) {
	t.Helper()

	configureGoose(t)
	if err := goose.DownTo(db, ".", version); err != nil {
		t.Fatalf("migrate down to %d: %v", version, err)
	}
}
