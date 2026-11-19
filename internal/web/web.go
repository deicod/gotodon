package web

import (
	"bufio"
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"strconv"
	"strings"
)

// Limits on the multipart/form-data bodies Bind reads. They follow Rack
// 3.2.7, which parses request bodies for Mastodon 4.5.3.
const (
	// maxMultipartBodyBytes caps a multipart body, file parts included. Rack
	// keeps up to 16 MiB of a body's non-file parts
	// (Rack::Multipart::Parser::BUFFERED_UPLOAD_BYTESIZE_LIMIT). Bind binds no
	// files, so the same cap covers the whole body.
	maxMultipartBodyBytes int64 = 16 << 20
	// maxMultipartParts caps the parts of a multipart body, as Rack's
	// multipart_total_part_limit does.
	maxMultipartParts = 4096
	// maxMultipartBoundaryLength is the longest boundary RFC 2046 §5.1.1
	// allows. Rack rejects longer ones as well.
	maxMultipartBoundaryLength = 70
)

// errInvalidMultipart reports a multipart/form-data body that cannot be
// parsed or has too many parts. It never matches io.EOF, which Bind returns
// for a body without fields.
var errInvalidMultipart = errors.New("invalid multipart/form-data body")

var textUnmarshalerType = reflect.TypeFor[encoding.TextUnmarshaler]()

// Bind decodes the request body into v.
//
// JSON bodies are decoded with encoding/json. Form bodies
// (application/x-www-form-urlencoded and multipart/form-data) are mapped onto
// v by form or json tag, with Rails-style keys: name[] for slices, and
// name[key] for struct fields and map entries. After a form body, r.PostForm
// and r.Form hold its fields as after r.ParseForm.
//
// A multipart body is read as a stream of at most maxMultipartBodyBytes and
// maxMultipartParts. File parts are skipped without being stored in memory or
// on disk; handlers that accept files parse their own bodies. A larger body
// fails with *http.MaxBytesError, without being read when its Content-Length
// already exceeds the limit and otherwise once reading passes it.
//
// Bind returns io.EOF when the body has no content or, for form bodies, no
// fields.
func Bind(r *http.Request, v interface{}) error {
	if r == nil {
		return errors.New("request is nil")
	}
	if v == nil {
		return errors.New("target is nil")
	}

	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "multipart/form-data") {
		values, err := readMultipartValues(r)
		if err != nil {
			return err
		}
		// ParseForm keeps a PostForm that is already set and adds the
		// query string to r.Form, as for urlencoded bodies.
		r.PostForm = values
		if err := r.ParseForm(); err != nil {
			return err
		}
		return bindForm(v, r.PostForm)
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	_ = r.Body.Close()

	if strings.HasPrefix(contentType, "application/json") {
		return decodeJSON(body, v)
	}

	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		r.Body = io.NopCloser(bytes.NewReader(body))
		if err := r.ParseForm(); err != nil {
			return err
		}
		return bindForm(v, r.PostForm)
	}

	if isLikelyJSON(body) {
		return decodeJSON(body, v)
	}

	r.Body = io.NopCloser(bytes.NewReader(body))
	if err := r.ParseForm(); err != nil {
		return err
	}
	return bindForm(v, r.PostForm)
}

// JSON writes a JSON response with the given status code.
func JSON(w http.ResponseWriter, status int, v interface{}) {
	JSONWithContentType(w, status, "application/json", v)
}

// JSONWithContentType writes v as JSON with the given status code under
// contentType, such as application/activity+json, replacing any Content-Type
// set before.
func JSONWithContentType(w http.ResponseWriter, status int, contentType string, v interface{}) {
	w.Header().Set("Content-Type", contentType)
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Fallback logging or ignore if write failed
		_ = err
	}
}

// AddVary adds the header name token to the Vary header of h unless Vary
// already lists it or is "*".
func AddVary(h http.Header, token string) {
	for _, value := range h.Values("Vary") {
		for _, listed := range strings.Split(value, ",") {
			listed = strings.TrimSpace(listed)
			if listed == "*" || strings.EqualFold(listed, token) {
				return
			}
		}
	}
	h.Add("Vary", token)
}

func decodeJSON(body []byte, v interface{}) error {
	if len(body) == 0 {
		return io.EOF
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	return decoder.Decode(v)
}

func isLikelyJSON(body []byte) bool {
	for _, b := range body {
		switch b {
		case ' ', '\n', '\r', '\t':
			continue
		case '{', '[':
			return true
		default:
			return false
		}
	}
	return false
}

// readMultipartValues reads the fields of r's multipart/form-data body,
// skipping file parts. See Bind for the limits.
func readMultipartValues(r *http.Request) (url.Values, error) {
	// The boundary is case-sensitive, so it comes from the header as sent.
	_, params, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errInvalidMultipart, err)
	}
	boundary := params["boundary"]
	if boundary == "" {
		return nil, fmt.Errorf("%w: %v", errInvalidMultipart, http.ErrMissingBoundary)
	}
	if len(boundary) > maxMultipartBoundaryLength {
		return nil, fmt.Errorf("%w: boundary longer than %d characters", errInvalidMultipart, maxMultipartBoundaryLength)
	}
	if r.ContentLength > maxMultipartBodyBytes {
		return nil, &http.MaxBytesError{Limit: maxMultipartBodyBytes}
	}

	values := url.Values{}
	if r.Body == nil {
		return values, nil
	}
	defer func() { _ = r.Body.Close() }()
	body := bufio.NewReader(http.MaxBytesReader(nil, r.Body, maxMultipartBodyBytes))
	if _, err := body.Peek(1); err != nil {
		if err == io.EOF {
			// No content at all: no fields, as for an empty urlencoded
			// body. Rack also returns no parameters here.
			return values, nil
		}
		return nil, multipartReadError(err)
	}

	reader := multipart.NewReader(body, boundary)
	for parts := 0; ; parts++ {
		part, err := reader.NextPart()
		// NextPart returns io.EOF itself only after the closing boundary.
		// A body that ends early fails with an error wrapping io.EOF.
		if err == io.EOF {
			return values, nil
		}
		if err != nil {
			return nil, multipartReadError(err)
		}
		if parts == maxMultipartParts {
			return nil, fmt.Errorf("%w: more than %d parts", errInvalidMultipart, maxMultipartParts)
		}
		name := part.FormName()
		if name == "" || isFilePart(part) {
			// The next NextPart call discards the rest of this part.
			continue
		}
		value, err := io.ReadAll(part)
		if err != nil {
			return nil, multipartReadError(err)
		}
		values.Add(name, string(value))
	}
}

// isFilePart reports whether part has a filename parameter. Rack treats such
// a part as a file even when the filename is empty, as browsers send for an
// empty file input.
func isFilePart(part *multipart.Part) bool {
	_, params, err := mime.ParseMediaType(part.Header.Get("Content-Disposition"))
	if err != nil {
		return false
	}
	_, ok := params["filename"]
	return ok
}

// multipartReadError returns the *http.MaxBytesError in err, if any, and
// otherwise err wrapped in errInvalidMultipart, without the io.EOF it may
// wrap.
func multipartReadError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return tooLarge
	}
	return fmt.Errorf("%w: %v", errInvalidMultipart, err)
}

func bindForm(target interface{}, values url.Values) error {
	if len(values) == 0 {
		return io.EOF
	}
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return errors.New("target must be a non-nil pointer")
	}
	rv = rv.Elem()
	switch rv.Kind() {
	case reflect.Struct:
		return bindStruct(rv, values)
	case reflect.Map:
		return bindMap(rv, values)
	default:
		return errors.New("target must point to a struct or a map")
	}
}

func bindStruct(rv reflect.Value, values url.Values) error {
	rt := rv.Type()
	for i := 0; i < rt.NumField(); i++ {
		field := rt.Field(i)
		if field.PkgPath != "" {
			continue
		}
		key := formKey(field)
		if key == "" {
			continue
		}
		fv := rv.Field(i)
		if !fv.CanSet() {
			continue
		}
		if _, err := bindField(fv, values, key); err != nil {
			return err
		}
	}
	return nil
}

// bindMap binds values onto mv, a map with string keys. Each entry binds like
// a struct field named by its key: home[last_read_id] sets last_read_id in
// the entry "home" of a map of structs, and follow sets the entry "follow" of
// a map of bools.
func bindMap(mv reflect.Value, values url.Values) error {
	mt := mv.Type()
	if mt.Key().Kind() != reflect.String {
		return errors.New("form map keys must be strings")
	}
	names := make(map[string]struct{}, len(values))
	for key := range values {
		if name, _, _ := strings.Cut(key, "["); name != "" {
			names[name] = struct{}{}
		}
	}
	for _, name := range slices.Sorted(maps.Keys(names)) {
		elem := reflect.New(mt.Elem()).Elem()
		bound, err := bindField(elem, values, name)
		if err != nil {
			return err
		}
		if !bound {
			continue
		}
		if mv.IsNil() {
			mv.Set(reflect.MakeMap(mt))
		}
		mv.SetMapIndex(reflect.ValueOf(name).Convert(mt.Key()), elem)
	}
	return nil
}

// bindField binds the values for key onto fv and reports whether values
// were present. Structs and maps bind from bracketed keys (key[name]), other
// types from the values of key itself.
func bindField(fv reflect.Value, values url.Values, key string) (bool, error) {
	switch {
	case fv.Kind() == reflect.Pointer && isNested(fv.Type().Elem()):
		nestedValues := nestedFormValues(values, key)
		if len(nestedValues) == 0 {
			return false, nil
		}
		if fv.IsNil() {
			fv.Set(reflect.New(fv.Type().Elem()))
		}
		return true, bindNested(fv.Elem(), nestedValues)
	case isNested(fv.Type()):
		nestedValues := nestedFormValues(values, key)
		if len(nestedValues) == 0 {
			return false, nil
		}
		return true, bindNested(fv, nestedValues)
	default:
		return setFieldFromValues(fv, values, key)
	}
}

// isNested reports whether a form field of type t binds from bracketed keys:
// a struct or map that does not unmarshal itself from text.
func isNested(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Struct, reflect.Map:
		return !reflect.PointerTo(t).Implements(textUnmarshalerType)
	default:
		return false
	}
}

func bindNested(v reflect.Value, values url.Values) error {
	if v.Kind() == reflect.Map {
		return bindMap(v, values)
	}
	return bindStruct(v, values)
}

func formKey(field reflect.StructField) string {
	if tag := strings.TrimSpace(field.Tag.Get("form")); tag != "" {
		if tag == "-" {
			return ""
		}
		return trimTagOptions(tag)
	}
	if tag := strings.TrimSpace(field.Tag.Get("json")); tag != "" {
		if tag == "-" {
			return ""
		}
		return trimTagOptions(tag)
	}
	return strings.ToLower(field.Name)
}

func trimTagOptions(tag string) string {
	if idx := strings.Index(tag, ","); idx >= 0 {
		return tag[:idx]
	}
	return tag
}

func nestedFormValues(values url.Values, key string) url.Values {
	if key == "" {
		return nil
	}
	prefix := key + "["
	nested := url.Values{}
	for formKey, formValues := range values {
		if !strings.HasPrefix(formKey, prefix) {
			continue
		}
		rest := strings.TrimPrefix(formKey, prefix)
		idx := strings.Index(rest, "]")
		if idx == -1 {
			continue
		}
		nestedKey := rest[:idx]
		remainder := rest[idx+1:]
		if nestedKey == "" {
			continue
		}
		if remainder != "" {
			nestedKey += remainder
		}
		nested[nestedKey] = append(nested[nestedKey], formValues...)
	}
	return nested
}

// setFieldFromValues sets field from the values of key and reports whether
// it was set.
func setFieldFromValues(field reflect.Value, values url.Values, key string) (bool, error) {
	vals, ok := values[key]
	if !ok && field.Kind() == reflect.Slice && !strings.HasSuffix(key, "[]") {
		vals, ok = values[key+"[]"]
	}
	if !ok {
		return false, nil
	}
	if field.Kind() == reflect.Pointer {
		if len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
			return false, nil
		}
		ptr := reflect.New(field.Type().Elem())
		if err := setValue(ptr.Elem(), vals); err != nil {
			return false, err
		}
		field.Set(ptr)
		return true, nil
	}
	return true, setValue(field, vals)
}

func setValue(field reflect.Value, vals []string) error {
	if !field.CanSet() {
		return nil
	}
	if field.Addr().IsValid() {
		if unmarshaler, ok := field.Addr().Interface().(encoding.TextUnmarshaler); ok {
			return unmarshaler.UnmarshalText([]byte(firstValue(vals)))
		}
	}
	if unmarshaler, ok := field.Interface().(encoding.TextUnmarshaler); ok {
		return unmarshaler.UnmarshalText([]byte(firstValue(vals)))
	}

	switch field.Kind() {
	case reflect.String:
		field.SetString(firstValue(vals))
	case reflect.Bool:
		value, err := parseBoolValue(firstValue(vals))
		if err != nil {
			return err
		}
		field.SetBool(value)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		value := strings.TrimSpace(firstValue(vals))
		if value == "" {
			field.SetInt(0)
			return nil
		}
		parsed, err := strconv.ParseInt(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetInt(parsed)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		value := strings.TrimSpace(firstValue(vals))
		if value == "" {
			field.SetUint(0)
			return nil
		}
		parsed, err := strconv.ParseUint(value, 10, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetUint(parsed)
	case reflect.Float32, reflect.Float64:
		value := strings.TrimSpace(firstValue(vals))
		if value == "" {
			field.SetFloat(0)
			return nil
		}
		parsed, err := strconv.ParseFloat(value, field.Type().Bits())
		if err != nil {
			return err
		}
		field.SetFloat(parsed)
	case reflect.Slice:
		return setSliceValue(field, vals)
	}
	return nil
}

func setSliceValue(field reflect.Value, vals []string) error {
	if field.Type().Elem().Kind() == reflect.String {
		slice := reflect.MakeSlice(field.Type(), 0, len(vals))
		for _, value := range vals {
			slice = reflect.Append(slice, reflect.ValueOf(value))
		}
		field.Set(slice)
		return nil
	}
	switch field.Type().Elem().Kind() {
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		slice := reflect.MakeSlice(field.Type(), 0, len(vals))
		bits := field.Type().Elem().Bits()
		for _, value := range vals {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			parsed, err := strconv.ParseInt(trimmed, 10, bits)
			if err != nil {
				return err
			}
			slice = reflect.Append(slice, reflect.ValueOf(parsed).Convert(field.Type().Elem()))
		}
		field.Set(slice)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		slice := reflect.MakeSlice(field.Type(), 0, len(vals))
		bits := field.Type().Elem().Bits()
		for _, value := range vals {
			trimmed := strings.TrimSpace(value)
			if trimmed == "" {
				continue
			}
			parsed, err := strconv.ParseUint(trimmed, 10, bits)
			if err != nil {
				return err
			}
			slice = reflect.Append(slice, reflect.ValueOf(parsed).Convert(field.Type().Elem()))
		}
		field.Set(slice)
		return nil
	}
	return nil
}

func parseBoolValue(value string) (bool, error) {
	trimmed := strings.TrimSpace(strings.ToLower(value))
	if trimmed == "" {
		return false, nil
	}
	switch trimmed {
	case "true", "1", "t", "yes", "y", "on":
		return true, nil
	case "false", "0", "f", "no", "n", "off":
		return false, nil
	default:
		return false, errors.New("invalid boolean value")
	}
}

func firstValue(vals []string) string {
	if len(vals) == 0 {
		return ""
	}
	return vals[0]
}

// JSONError writes a standardized error JSON response.
func JSONError(w http.ResponseWriter, status int, message string) {
	JSON(w, status, map[string]string{"error": message})
}

// Param returns a path value from the request (Go 1.22+).
func Param(r *http.Request, key string) string {
	return r.PathValue(key)
}

// QueryParam returns a query parameter value.
func QueryParam(r *http.Request, key string) string {
	return r.URL.Query().Get(key)
}

// Int64Param parses an int64 path parameter.
func Int64Param(r *http.Request, key string) (int64, error) {
	val := r.PathValue(key)
	return strconv.ParseInt(val, 10, 64)
}

// ParsePagination returns limit and offset from query parameters.
func ParsePagination(r *http.Request) (int, int) {
	limitStr := QueryParam(r, "limit")
	limit, _ := strconv.Atoi(limitStr)
	if limit < 0 {
		limit = 0
	}

	offsetStr := QueryParam(r, "offset")
	offset, _ := strconv.Atoi(offsetStr)
	if offset < 0 {
		offset = 0
	}
	return limit, offset
}
