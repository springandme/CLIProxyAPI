package managementasset

import _ "embed"

//go:embed builtin/codex-inspection.html
var builtinCodexInspectionHTML []byte

// BuiltinCodexInspectionHTML returns the bundled standalone Codex inspection panel.
func BuiltinCodexInspectionHTML() []byte {
	return builtinCodexInspectionHTML
}
