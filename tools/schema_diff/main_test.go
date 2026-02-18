package main

import "testing"

func TestParseSQLIndex_Basic(t *testing.T) {
	stmt := "CREATE UNIQUE INDEX index_account_aliases_on_account_id_and_uri ON account_aliases (account_id, uri);"
	idx, table := parseSQLIndex(stmt)
	if table != "account_aliases" {
		t.Fatalf("table = %q, want %q", table, "account_aliases")
	}
	if idx.Name != "index_account_aliases_on_account_id_and_uri" {
		t.Fatalf("index name = %q, want %q", idx.Name, "index_account_aliases_on_account_id_and_uri")
	}
	if !idx.Unique {
		t.Fatalf("index unique = false, want true")
	}
	if len(idx.Columns) != 2 || idx.Columns[0] != "account_id" || idx.Columns[1] != "uri" {
		t.Fatalf("columns = %#v, want [account_id uri]", idx.Columns)
	}
}

func TestParseSQLMigrations_FindsIndexes(t *testing.T) {
	schema, err := parseSQLMigrations("../../db/migrations")
	if err != nil {
		t.Fatalf("parseSQLMigrations error: %v", err)
	}
	table := schema.Tables["account_aliases"]
	if table == nil {
		t.Fatalf("account_aliases table missing")
	}
	if _, ok := table.Indexes["index_account_aliases_on_account_id_and_uri"]; !ok {
		t.Fatalf("index_account_aliases_on_account_id_and_uri missing")
	}
}
