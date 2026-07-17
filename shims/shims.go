// Package shims embeds the client-side protocol shims so `wand init` can drop
// them into a project without needing the wand source tree on disk. The shims
// themselves are thin protocol translators — all mode logic lives in the proxy.
package shims

import "embed"

//go:embed python/*.py
var pythonFS embed.FS

// PythonModules lists the python shim modules installable into a project,
// keyed by their file name. Order is stable for deterministic install output.
// The conftest is intentionally excluded: it lives at the project root, not in
// the tool-managed shim directory (see PythonConftest).
var PythonModules = []string{"client.py", "marks.py", "boto3_shim.py"}

// PythonShim returns the embedded contents of a python shim module by file name
// (e.g. "client.py").
func PythonShim(name string) ([]byte, error) {
	return pythonFS.ReadFile("python/" + name)
}

// PythonConftest returns the embedded pytest conftest that activates the boto3
// bridge for a whole test session. Unlike the shim modules it is installed to
// the project root so pytest discovers it, and only when boto3 is in use.
func PythonConftest() ([]byte, error) {
	return pythonFS.ReadFile("python/conftest.py")
}
