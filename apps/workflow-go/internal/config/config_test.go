package config

import (
	"strings"
	"testing"
)

func TestValidateAPI(t *testing.T) {
	good := Config{JWTAccessSecret: strings.Repeat("k", 48), DatabaseURL: "postgres://dataflow@db/x", AppDatabaseURL: "postgres://dataflow_app@db/x", AppDatabaseURLSet: true}
	if warnings, err := good.ValidateAPI(); err != nil || len(warnings) != 0 {
		t.Fatalf("good config: warnings=%v err=%v", warnings, err)
	}
	for name, mutate := range map[string]func(*Config){
		"default secret":       func(c *Config) { c.JWTAccessSecret = DefaultJWTAccessSecret },
		"short secret":         func(c *Config) { c.JWTAccessSecret = "short" },
		"no app url":           func(c *Config) { c.AppDatabaseURLSet = false; c.AppDatabaseURL = c.DatabaseURL },
		"app url is superuser": func(c *Config) { c.AppDatabaseURL = c.DatabaseURL },
	} {
		dev := good
		mutate(&dev)
		if warnings, err := dev.ValidateAPI(); err != nil || len(warnings) != 1 {
			t.Errorf("%s in development: warnings=%v err=%v, want one warning", name, warnings, err)
		}
		prod := dev
		prod.Production = true
		if _, err := prod.ValidateAPI(); err == nil {
			t.Errorf("%s in production: started", name)
		}
	}
}
