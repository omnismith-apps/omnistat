package config

import _ "embed" // the starter file

// Starter is the commented configuration file `omnistat service install`
// writes when none exists (spec 007 FR-014). Loaded as is, it leaves every
// default in force.
//
//go:embed starter.yaml
var Starter []byte
