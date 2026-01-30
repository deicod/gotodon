package web

import (
	"bytes"
	"encoding"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
)

// Bind decodes the request body into v.
func Bind(r *http.Request, v interface{}) error {
	if r == nil {
		return errors.New("request is nil")
	}
	if v == nil {
		return errors.New("target is nil")
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		return err
	}
	_ = r.Body.Close()

	contentType := strings.ToLower(r.Header.Get("Content-Type"))
	if strings.HasPrefix(contentType, "application/json") {
		return decodeJSON(body, v)
	}

	if strings.HasPrefix(contentType, "application/x-www-form-urlencoded") ||
		strings.HasPrefix(contentType, "multipart/form-data") {
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
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// Fallback logging or ignore if write failed
		_ = err
	}
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

func bindForm(target interface{}, values url.Values) error {
	if len(values) == 0 {
		return io.EOF
	}
	rv := reflect.ValueOf(target)
	if rv.Kind() != reflect.Ptr || rv.IsNil() {
		return errors.New("target must be a non-nil pointer")
	}
	rv = rv.Elem()
	if rv.Kind() != reflect.Struct {
		return errors.New("target must point to a struct")
	}
	return bindStruct(rv, values)
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
		if fv.Kind() == reflect.Ptr && fv.Type().Elem().Kind() == reflect.Struct {
			nestedValues := nestedFormValues(values, key)
			if len(nestedValues) == 0 {
				continue
			}
			if fv.IsNil() {
				fv.Set(reflect.New(fv.Type().Elem()))
			}
			if err := bindStruct(fv.Elem(), nestedValues); err != nil {
				return err
			}
			continue
		}
		if err := setFieldFromValues(fv, values, key); err != nil {
			return err
		}
	}
	return nil
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

func setFieldFromValues(field reflect.Value, values url.Values, key string) error {
	vals, ok := values[key]
	if !ok && field.Kind() == reflect.Slice && !strings.HasSuffix(key, "[]") {
		vals, ok = values[key+"[]"]
	}
	if !ok {
		return nil
	}
	if field.Kind() == reflect.Ptr {
		if len(vals) == 0 || strings.TrimSpace(vals[0]) == "" {
			return nil
		}
		ptr := reflect.New(field.Type().Elem())
		if err := setValue(ptr.Elem(), vals); err != nil {
			return err
		}
		field.Set(ptr)
		return nil
	}
	return setValue(field, vals)
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
