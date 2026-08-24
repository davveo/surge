package main

import (
	"fmt"
	"testing"
)

func TestIsIdempotentSchemaErr(t *testing.T) {
	if !isIdempotentSchemaErr(fmt.Errorf("Error 1060: Duplicate column name 'title'")) {
		t.Fatal("duplicate column")
	}
	if !isIdempotentSchemaErr(fmt.Errorf("Table 'user_settings' already exists")) {
		t.Fatal("already exists")
	}
	if isIdempotentSchemaErr(fmt.Errorf("syntax error")) {
		t.Fatal("real errors must surface")
	}
}
