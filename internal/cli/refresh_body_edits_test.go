package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParsePRSelectionAcceptsListFileAndManifest(t *testing.T) {
	dir := t.TempDir()
	list := filepath.Join(dir, "prs.txt")
	os.WriteFile(list, []byte("# shortlist\n30\n10\n\n20\n10\n"), 0644)
	manifest := filepath.Join(dir, "cohort-manifest.json")
	os.WriteFile(manifest, []byte(`{"schema_version":"cohort-manifest/v2","pairs":[{"id":"pair-0001","positive":101,"negative":102},{"id":"pair-0002","positive":103,"negative":104}]}`), 0644)
	notManifest := filepath.Join(dir, "other.json")
	os.WriteFile(notManifest, []byte(`{"schema_version":"flip-report/v1"}`), 0644)

	cases := []struct {
		arg  string
		want []int
		err  string
	}{
		{"3, 1,2", []int{1, 2, 3}, ""},
		{list, []int{10, 20, 30}, ""},
		{manifest, []int{101, 102, 103, 104}, ""},
		{notManifest, nil, "not a cohort manifest"},
		{"1,x", nil, "invalid PR number"},
		{"", nil, "required"},
	}
	for _, c := range cases {
		got, err := parsePRSelection(c.arg)
		if c.err != "" {
			if err == nil || !strings.Contains(err.Error(), c.err) {
				t.Errorf("%q: err = %v, want containing %q", c.arg, err, c.err)
			}
			continue
		}
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%q: got %v (%v), want %v", c.arg, got, err, c.want)
		}
	}
}
