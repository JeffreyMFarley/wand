// Package shims embeds the client-side protocol shims so `wand init` can drop
// them into a project without needing the wand source tree on disk. The shims
// themselves are thin protocol translators — all mode logic lives in the proxy.
package shims

import "embed"

//go:embed python/*.py
var pythonFS embed.FS

// PythonModules lists the python shim modules installable into a project,
// keyed by their file name. Order is stable for deterministic install output.
var PythonModules = []string{"client.py", "marks.py", "boto3_shim.py"}

// PythonShim returns the embedded contents of a python shim module by file name
// (e.g. "client.py").
func PythonShim(name string) ([]byte, error) {
	return pythonFS.ReadFile("python/" + name)
}
