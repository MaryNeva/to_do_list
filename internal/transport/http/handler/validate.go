package handler

import (
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// newValidator reports a field by its JSON name, so an error body names what
// the client actually sent rather than the Go struct field.
func newValidator() *validator.Validate {
	v := validator.New()
	v.RegisterTagNameFunc(func(field reflect.StructField) string {
		name, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if name == "-" {
			return ""
		}
		return name
	})
	return v
}
