//go:build embedded

package main

import _ "embed"

//go:embed runtime.zip
var embeddedRuntimeZip []byte
