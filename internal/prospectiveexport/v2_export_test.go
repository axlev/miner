package prospectiveexport

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestExportMetadataVersionSelectsTheContract pins the two selectable paths: the
// library default and "v1" reproduce the pilot's reviewer-metadata/v1 bundle, "v2"
// writes reviewer-metadata/v2 and records per-field admission in the audit, and an
// unknown selector is refused before anything is written.
func TestExportMetadataVersionSelectsTheContract(t *testing.T) {
	dir := t.TempDir()
	fx := writeFixture(t, dir)
	run := func(name, version string) (string, Audit) {
		out := filepath.Join(dir, name)
		opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: out, EvaluatorOut: out + "-evaluator", MetadataVersion: version}
		if err := Export(context.Background(), opt); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, err := os.ReadFile(bundlePath(out, "reviewer/metadata.json"))
		if err != nil {
			t.Fatal(err)
		}
		var meta map[string]any
		json.Unmarshal(raw, &meta)
		ab, err := os.ReadFile(filepath.Join(out+"-evaluator", "metadata-export-audit.json"))
		if err != nil {
			t.Fatal(err)
		}
		var audit Audit
		json.Unmarshal(ab, &audit)
		return meta["schema_version"].(string), audit
	}

	v, a := run("default", "")
	if v != MetadataSchemaVersion || a.MetadataVersion != MetadataSchemaVersion || a.Admission != nil {
		t.Errorf("default path: schema %q audit %+v", v, a)
	}
	v, a = run("v1", MetadataVersionV1)
	if v != MetadataSchemaVersion || a.Admission != nil {
		t.Errorf("v1 path: schema %q audit admission %+v", v, a.Admission)
	}
	v, a = run("v2", MetadataVersionV2)
	if v != MetadataSchemaVersionV2 || a.MetadataVersion != MetadataSchemaVersionV2 {
		t.Errorf("v2 path: schema %q audit %+v", v, a)
	}
	for _, f := range []string{"title", "description"} {
		if a.Admission[f].Outcome == "" || a.Fields[f].Outcome != a.Admission[f].Outcome {
			t.Errorf("v2 audit must record %s admission consistently: admission=%+v field=%+v", f, a.Admission[f], a.Fields[f])
		}
	}

	bad := filepath.Join(dir, "bad")
	opt := Options{CorrelatedInput: fx.input, CacheFile: fx.cache, Repo: fx.repoDir, PR: 42, Cutoff: cutoff, ProspectiveOut: bad, EvaluatorOut: bad + "-evaluator", MetadataVersion: "v9"}
	if err := Export(context.Background(), opt); err == nil {
		t.Fatal("unknown metadata version accepted")
	}
	if _, err := os.Stat(bad); !os.IsNotExist(err) {
		t.Errorf("output written despite refused version")
	}
}
