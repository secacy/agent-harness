package task

import (
	"reflect"
	"testing"
)

func TestBuildConfigurationArgumentsAndValidation(t *testing.T) {
	args := []string{"go", "test", "-json", "./..."}
	c := BuildConfiguration{ID: "alternate", Tags: []string{"rpcx_alternate", "integration"}}
	got := c.Arguments(args)
	if !reflect.DeepEqual(got, []string{"go", "test", "-tags=rpcx_alternate,integration", "-json", "./..."}) || len(args) != 4 || args[2] != "-json" {
		t.Fatal("incorrect command arguments:", got, args)
	}
	if err := ValidateBuildConfigurations([]BuildConfiguration{{ID: "default"}, c}); err != nil {
		t.Fatal(err)
	}
	for _, configs := range [][]BuildConfiguration{
		{{ID: "default"}, {ID: "default"}},
		{{ID: "../outside"}},
		{{ID: "alternate", Tags: []string{"a", "a"}}},
		{{ID: "alternate", Tags: []string{"a,b"}}},
	} {
		if err := ValidateBuildConfigurations(configs); err == nil {
			t.Fatal("invalid host configuration accepted:", configs)
		}
	}
}
