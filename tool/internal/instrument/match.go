// Copyright The OpenTelemetry Authors
// SPDX-License-Identifier: Apache-2.0

package instrument

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"

	"go.opentelemetry.io/otelc/tool/ex"
	"go.opentelemetry.io/otelc/tool/internal/rule"
	"go.opentelemetry.io/otelc/tool/util"
)

// load loads the matched rules from the build temp directory.
// TODO: Shared memory across all sub-processes is possible
func (ip *instrumentPhase) load() ([]*rule.InstRuleSet, error) {
	f := util.GetMatchedRuleFile()
	content, err := os.ReadFile(f)
	if os.IsNotExist(err) {
		return nil, ex.Newf("no instrumentation configuration found (%s does not exist); "+
			"run `otelc setup` in the module directory before building with `-toolexec`, "+
			"or build with `otelc go build` instead", f)
	}
	if err != nil {
		return nil, ex.Wrapf(err, "failed to read file %s", f)
	}
	rset := make([]*rule.InstRuleSet, 0)
	err = json.Unmarshal(content, &rset)
	if err != nil {
		return nil, ex.Wrapf(err, "failed to unmarshal JSON")
	}

	ip.Debug("Load matched rule sets", "path", f)
	return rset, nil
}

// cannotMatch reports whether no rule set in matched.json can be for the
// package importPath, without decoding the file: a match needs a set whose
// module_path equals importPath, and json.Marshal writes that field in the
// same encoding used here. Any read problem returns false so load reports it.
func cannotMatch(importPath string) (bool, error) {
	if importPath == "" {
		return false, nil
	}
	content, err := os.ReadFile(util.GetMatchedRuleFile())
	if err != nil {
		return false, nil //nolint:nilerr // load reports the error
	}
	encoded, err := json.Marshal(importPath)
	if err != nil {
		return false, ex.Wrapf(err, "encoding import path %q", importPath)
	}
	needle := append([]byte(`"module_path":`), encoded...)
	return !bytes.Contains(content, needle), nil
}

// anyRuleSetHasIgnoredCallFiles reports whether any package in this build has
// a //otelc:ignore comment above a call, found during setup.
func anyRuleSetHasIgnoredCallFiles(allSet []*rule.InstRuleSet) bool {
	for _, rset := range allSet {
		if len(rset.IgnoredCallFiles) > 0 {
			return true
		}
	}
	return false
}

// loadImportNames loads the import name table. A missing file is not
// an error, since the table only improves a fallback guess.
func loadImportNames() (map[string]string, error) {
	f := util.GetImportNamesFile()
	content, err := os.ReadFile(f)
	if os.IsNotExist(err) {
		return map[string]string{}, nil
	}
	if err != nil {
		return nil, ex.Wrapf(err, "failed to read file %s", f)
	}
	names := make(map[string]string)
	if jsonErr := json.Unmarshal(content, &names); jsonErr != nil {
		return nil, ex.Wrapf(jsonErr, "failed to unmarshal JSON")
	}
	return names, nil
}

// match matches the rules with the compile command.
func (ip *instrumentPhase) match(allSet []*rule.InstRuleSet, args []string) *rule.InstRuleSet {
	// Several packages can share an import path, such as every main package
	// of a build, so a rule set only matches a compile that holds the files
	// its rules were matched in. A go test run can also compile one package
	// twice, with and without its _test.go files, so the rule set must come
	// from the same kind of compile as this one.
	importPath := util.FindFlagValue(args, "-p")
	util.Assert(importPath != "", "sanity check")
	isTest := util.IsTestBuild(args)
	compiled := make(map[string]bool, len(args))
	for _, arg := range args {
		if !util.IsGoFile(arg) {
			continue
		}
		if abs, err := filepath.Abs(arg); err == nil {
			compiled[abs] = true
		}
	}
	for _, rset := range allSet {
		if rset.ModulePath == importPath && rset.IsTest == isTest && ip.compilesRuleFiles(rset, compiled) {
			ip.Debug("Match rule set", "set", rset)
			return rset
		}
	}
	return nil
}

// compilesRuleFiles reports whether every file rset has rules for is in
// compiled, using the same cgo file mapping as instrument.
func (ip *instrumentPhase) compilesRuleFiles(rset *rule.InstRuleSet, compiled map[string]bool) bool {
	_, files := groupRules(ip.workDir, rset)
	for _, file := range files {
		if !compiled[file] {
			return false
		}
	}
	return true
}
