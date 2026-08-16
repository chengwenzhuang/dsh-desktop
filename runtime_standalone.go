//go:build !embedded

package main

// embeddedRuntimeZip is empty in the slim build: the app then relies on a
// system Node.js installation (with automatic dsh bootstrap as a fallback).
var embeddedRuntimeZip []byte
