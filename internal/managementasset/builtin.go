package managementasset

import _ "embed"

//go:embed builtin/management.html
var builtinManagementHTML []byte

// BuiltinManagementHTML returns the management panel bundled with this binary.
func BuiltinManagementHTML() []byte {
	return builtinManagementHTML
}
