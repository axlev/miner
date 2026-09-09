package gitx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// SnapshotEntry describes one regular file materialized into a source snapshot.
type SnapshotEntry struct {
	// Path is the repository-relative, slash-separated path of the file.
	Path string
	// Mode is the Git file mode, always "100644" or "100755"; every other mode is
	// rejected rather than materialized.
	Mode string
	// Blob is the SHA-1 of the file's content object.
	Blob string
	// Size is the number of bytes written.
	Size int64
}

// snapshotRejectedNames are filesystem names that a consumer treats as Git history,
// alternates, submodule or worktree leakage. A materialized snapshot is a plain
// directory tree, so an entry carrying one of these names cannot be published: the
// snapshot would be rejected downstream, and silently dropping the entry would make
// the snapshot stop corresponding to the patch generated from the same tree.
var snapshotRejectedNames = map[string]bool{
	".git": true, ".gitmodules": true, "packed-refs": true, "HEAD": true,
	"ORIG_HEAD": true, "FETCH_HEAD": true, "MERGE_HEAD": true, "shallow": true,
	"objects": true, "refs": true, "reflogs": true, "worktrees": true, "alternates": true,
}

// safeSnapshotPath rejects any tree path that would escape, or resolve outside, the
// destination directory once joined to it.
func safeSnapshotPath(p string) error {
	if p == "" {
		return fmt.Errorf("empty tree path")
	}
	if strings.HasPrefix(p, "/") || filepath.IsAbs(p) {
		return fmt.Errorf("absolute tree path %q", p)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("tree path %q contains a non-literal segment", p)
		}
	}
	return nil
}

// ListTree returns every blob in the tree of commitish, sorted by path. It fails
// closed on any entry that cannot be published as a plain regular file — a symlink
// (which can point anywhere, including at evaluator-only material), a submodule
// gitlink, or a name a consumer reads as Git metadata — rather than skipping it,
// because a skipped entry would leave the snapshot inconsistent with a patch
// generated from the same tree.
func (r *Repository) ListTree(ctx context.Context, commitish string) ([]SnapshotEntry, error) {
	out, err := r.gitOutput(ctx, "ls-tree", "-r", "-z", "--full-tree", commitish+"^{tree}")
	if err != nil {
		return nil, err
	}
	var entries []SnapshotEntry
	var rejected []string
	for _, rec := range strings.Split(string(out), "\x00") {
		if rec == "" {
			continue
		}
		tab := strings.IndexByte(rec, '\t')
		if tab < 0 {
			return nil, fmt.Errorf("unparsable ls-tree record %q", rec)
		}
		fields := strings.Fields(rec[:tab])
		if len(fields) != 3 {
			return nil, fmt.Errorf("unparsable ls-tree header %q", rec[:tab])
		}
		mode, objType, blob, path := fields[0], fields[1], fields[2], rec[tab+1:]
		switch {
		case mode == "120000":
			rejected = append(rejected, path+" (symlink)")
		case mode == "160000" || objType == "commit":
			rejected = append(rejected, path+" (submodule gitlink)")
		case objType != "blob" || (mode != "100644" && mode != "100755"):
			rejected = append(rejected, fmt.Sprintf("%s (unsupported %s mode %s)", path, objType, mode))
		default:
			if err := safeSnapshotPath(path); err != nil {
				return nil, err
			}
			for _, seg := range strings.Split(path, "/") {
				if snapshotRejectedNames[seg] {
					rejected = append(rejected, fmt.Sprintf("%s (path segment %q reads as Git metadata)", path, seg))
					break
				}
			}
			entries = append(entries, SnapshotEntry{Path: path, Mode: mode, Blob: blob})
		}
	}
	if len(rejected) > 0 {
		sort.Strings(rejected)
		return nil, fmt.Errorf("tree of %s has %d entr(ies) that cannot be published as a plain source snapshot: %s", commitish, len(rejected), strings.Join(rejected, ", "))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })
	return entries, nil
}

// MaterializeTree writes the complete tree of commitish into destDir as a plain
// directory of regular files, reading content straight from the object database so
// no working tree, checkout, or `.git` directory is involved. destDir must not exist.
//
// The result is exactly the tree named by commitish: a patch produced by diffing
// another commit against the same commitish therefore describes precisely this
// directory, with no separate reconciliation step that could drift.
func (r *Repository) MaterializeTree(ctx context.Context, commitish, destDir string) ([]SnapshotEntry, error) {
	entries, err := r.ListTree(ctx, commitish)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(destDir); err == nil {
		return nil, fmt.Errorf("snapshot destination %q already exists", destDir)
	} else if !os.IsNotExist(err) {
		return nil, err
	}
	if err := os.MkdirAll(destDir, 0755); err != nil {
		return nil, err
	}

	cmd := exec.CommandContext(ctx, "git", "--git-dir", r.RepoDir, "cat-file", "--batch")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	// Requests are streamed while responses are consumed, so a large tree never has
	// to be buffered in memory on either side of the pipe.
	writeErr := make(chan error, 1)
	go func() {
		w := bufio.NewWriter(stdin)
		for _, e := range entries {
			if _, err := w.WriteString(e.Blob + "\n"); err != nil {
				writeErr <- err
				_ = stdin.Close()
				return
			}
		}
		err := w.Flush()
		if cerr := stdin.Close(); err == nil {
			err = cerr
		}
		writeErr <- err
	}()

	readErr := func() error {
		br := bufio.NewReaderSize(stdout, 1<<16)
		for i := range entries {
			e := &entries[i]
			header, err := br.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read object header for %s: %w", e.Path, err)
			}
			parts := strings.Fields(strings.TrimSuffix(header, "\n"))
			if len(parts) != 3 || parts[1] != "blob" {
				return fmt.Errorf("unexpected cat-file response %q for %s", strings.TrimSpace(header), e.Path)
			}
			var size int64
			if _, err := fmt.Sscanf(parts[2], "%d", &size); err != nil || size < 0 {
				return fmt.Errorf("unparsable object size %q for %s", parts[2], e.Path)
			}
			full := filepath.Join(destDir, filepath.FromSlash(e.Path))
			if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
				return err
			}
			perm := os.FileMode(0644)
			if e.Mode == "100755" {
				perm = 0755
			}
			f, err := os.OpenFile(full, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perm)
			if err != nil {
				return err
			}
			if _, err := io.CopyN(f, br, size); err != nil {
				f.Close()
				return fmt.Errorf("write %s: %w", e.Path, err)
			}
			if err := f.Close(); err != nil {
				return err
			}
			e.Size = size
			// git cat-file --batch terminates every object payload with a newline.
			if b, err := br.ReadByte(); err != nil || b != '\n' {
				return fmt.Errorf("missing object terminator after %s", e.Path)
			}
		}
		return nil
	}()

	_, _ = io.Copy(io.Discard, stdout)
	wErr := <-writeErr
	cmdErr := cmd.Wait()
	if readErr != nil {
		return nil, readErr
	}
	if wErr != nil {
		return nil, wErr
	}
	if cmdErr != nil {
		return nil, fmt.Errorf("git cat-file --batch failed: %s (%w)", strings.TrimSpace(stderr.String()), cmdErr)
	}
	return entries, nil
}
