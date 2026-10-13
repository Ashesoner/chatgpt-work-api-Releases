package mcpserver

import (
	"reflect"
	"testing"
)

func TestLoadSkillInputSchemaRemainsNameOnly(t *testing.T) {
	typ := reflect.TypeOf(LoadSkillInput{})
	if typ.NumField() != 1 {
		t.Fatalf("load_skill input schema changed: got %d fields", typ.NumField())
	}
	field := typ.Field(0)
	if field.Name != "Name" || field.Tag.Get("json") != "name" {
		t.Fatalf("unexpected load_skill input field: %s json=%q", field.Name, field.Tag.Get("json"))
	}
}
