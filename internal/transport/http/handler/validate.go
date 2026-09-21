package handler

import (
	"reflect"
	"strings"

	"github.com/go-playground/validator/v10"
)

// newValidator reports fields by their JSON names, not Go field names.
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
