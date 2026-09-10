package gitx

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// SnapshotEntry describes one entry materialized into a source snapshot.
type SnapshotEntry struct {
	// Path is the repository-relative, slash-separated path of the entry.
	Path string
	// Mode is the Git file mode: "100644", "100755", or "120000" for a symlink.
	// Every other mode is rejected rather than materialized.
	Mode string
	// Blob is the SHA-1 of the entry's content object.
	Blob string
	// Size is the blob's size in bytes, as reported by the object database.
	Size int64
	// LinkTarget is the symlink's target path, empty for a regular file.
	LinkTarget string
}

// IsSymlink reports whether this entry is materialized as a symbolic link.
func (e SnapshotEntry) IsSymlink() bool { return e.Mode == "120000" }

// snapshotRejectedNames are filesystem names a consumer treats as Git history,
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

// validateSymlinks admits a symlink only when it is provably confined to the
// snapshot, and rejects the whole tree otherwise.
//
// The conditions are the ones engine-runner's boundary validator enforces on the
// published bundle. They are applied here so the exporter never builds a snapshot
// that would be refused at ingest — this is the exporter deciding what it may emit,
// not a copy of the engine's rule tables.
//
// Resolution is lexical, against the Git tree listing rather than the filesystem.
// Nothing is materialized or followed while checking, so a link cannot be walked out
// of the tree during validation.
func validateSymlinks(entries []SnapshotEntry) error {
	byPath := make(map[string]SnapshotEntry, len(entries))
	dirs := map[string]bool{}
	for _, e := range entries {
		byPath[e.Path] = e
		for d := path.Dir(e.Path); d != "." && d != "/"; d = path.Dir(d) {
			dirs[d] = true
		}
	}

	var bad []string
	for _, e := range entries {
		if !e.IsSymlink() {
			continue
		}
		target := e.LinkTarget
		switch {
		case target == "":
			bad = append(bad, fmt.Sprintf("%s (empty target)", e.Path))
			continue
		case strings.HasPrefix(target, "/"):
			bad = append(bad, fmt.Sprintf("%s (absolute target %q)", e.Path, target))
			continue
		}
		// Resolve against the link's own directory, lexically.
		resolved := path.Clean(path.Join(path.Dir(e.Path), target))
		if resolved == ".." || strings.HasPrefix(resolved, "../") || path.IsAbs(resolved) {
			bad = append(bad, fmt.Sprintf("%s (target %q escapes the snapshot)", e.Path, target))
			continue
		}
		t, isFile := byPath[resolved]
		if !isFile && !dirs[resolved] {
			bad = append(bad, fmt.Sprintf("%s (target %q does not exist in the tree)", e.Path, target))
			continue
		}
		// Chains are rejected outright rather than followed to a depth limit: a rule
		// with no traversal loop has no traversal bug.
		if isFile && t.IsSymlink() {
			bad = append(bad, fmt.Sprintf("%s (target %q is itself a symlink; chains are not admissible)", e.Path, target))
			continue
		}
	}
	if len(bad) > 0 {
		sort.Strings(bad)
		return fmt.Errorf("tree has %d inadmissible symlink(s): %s", len(bad), strings.Join(bad, ", "))
	}
	return nil
}

// ListTree returns every entry in the tree of commitish, sorted by path. It fails
// closed on anything that cannot be published as part of a plain source snapshot: a
// submodule gitlink, an unsupported mode, a Git-metadata name, or a symlink that is
// not provably confined to the tree. Rejection is preferred to skipping, because a
// skipped entry would leave the snapshot inconsistent with a patch generated from the
// same tree, and nothing downstream re-checks that correspondence.
func (r *Repository) ListTree(ctx context.Context, commitish string) ([]SnapshotEntry, error) {
	// -l adds the blob size, so a caller can size a snapshot without materializing it.
	out, err := r.gitOutput(ctx, "ls-tree", "-r", "-l", "-z", "--full-tree", commitish+"^{tree}")
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
		if len(fields) != 4 {
			return nil, fmt.Errorf("unparsable ls-tree header %q", rec[:tab])
		}
		mode, objType, blob, sizeField, p := fields[0], fields[1], fields[2], fields[3], rec[tab+1:]
		// Non-blob entries report "-" for size; they are rejected below regardless.
		var size int64
		if sizeField != "-" {
			n, err := strconv.ParseInt(sizeField, 10, 64)
			if err != nil {
				return nil, fmt.Errorf("unparsable ls-tree size %q for %q", sizeField, p)
			}
			size = n
		}
		switch {
		case mode == "160000" || objType == "commit":
			rejected = append(rejected, p+" (submodule gitlink)")
		case objType != "blob" || (mode != "100644" && mode != "100755" && mode != "120000"):
			rejected = append(rejected, fmt.Sprintf("%s (unsupported %s mode %s)", p, objType, mode))
		default:
			if err := safeSnapshotPath(p); err != nil {
				return nil, err
			}
			blocked := false
			for _, seg := range strings.Split(p, "/") {
				if snapshotRejectedNames[seg] {
					rejected = append(rejected, fmt.Sprintf("%s (path segment %q reads as Git metadata)", p, seg))
					blocked = true
					break
				}
			}
			if !blocked {
				entries = append(entries, SnapshotEntry{Path: p, Mode: mode, Blob: blob, Size: size})
			}
		}
	}
	if len(rejected) > 0 {
		sort.Strings(rejected)
		return nil, fmt.Errorf("tree of %s has %d entr(ies) that cannot be published as a plain source snapshot: %s", commitish, len(rejected), strings.Join(rejected, ", "))
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Path < entries[j].Path })

	// A symlink's target is its blob content, so the targets have to be read before
	// the links can be judged admissible.
	if err := r.loadLinkTargets(ctx, entries); err != nil {
		return nil, err
	}
	if err := validateSymlinks(entries); err != nil {
		return nil, fmt.Errorf("tree of %s: %w", commitish, err)
	}
	return entries, nil
}

// loadLinkTargets fills LinkTarget for every symlink entry, reading the target
// strings from the object database in one batch.
func (r *Repository) loadLinkTargets(ctx context.Context, entries []SnapshotEntry) error {
	var idx []int
	for i := range entries {
		if entries[i].IsSymlink() {
			idx = append(idx, i)
		}
	}
	if len(idx) == 0 {
		return nil
	}
	var stdin strings.Builder
	for _, i := range idx {
		stdin.WriteString(entries[i].Blob + "\n")
	}
	cmd := exec.CommandContext(ctx, "git", "--git-dir", r.RepoDir, "cat-file", "--batch")
	cmd.Stdin = strings.NewReader(stdin.String())
	var stderr strings.Builder
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return fmt.Errorf("read symlink targets: %s (%w)", strings.TrimSpace(stderr.String()), err)
	}
	br := bufio.NewReader(strings.NewReader(string(out)))
	for _, i := range idx {
		header, err := br.ReadString('\n')
		if err != nil {
			return fmt.Errorf("read symlink target header for %s: %w", entries[i].Path, err)
		}
		parts := strings.Fields(strings.TrimSuffix(header, "\n"))
		if len(parts) != 3 || parts[1] != "blob" {
			return fmt.Errorf("unexpected cat-file response %q for %s", strings.TrimSpace(header), entries[i].Path)
		}
		size, err := strconv.ParseInt(parts[2], 10, 64)
		if err != nil || size < 0 {
			return fmt.Errorf("unparsable symlink target size %q for %s", parts[2], entries[i].Path)
		}
		buf := make([]byte, size)
		if _, err := io.ReadFull(br, buf); err != nil {
			return fmt.Errorf("read symlink target for %s: %w", entries[i].Path, err)
		}
		entries[i].LinkTarget = string(buf)
		if b, err := br.ReadByte(); err != nil || b != '\n' {
			return fmt.Errorf("missing object terminator after %s", entries[i].Path)
		}
	}
	return nil
}

// MaterializeTree writes the complete tree of commitish into destDir as a plain
// directory of regular files and in-tree symlinks, reading content straight from the
// object database so no working tree, checkout, or `.git` directory is involved.
// destDir must not exist.
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

	// Symlinks carry their content in LinkTarget already, so only regular files are
	// streamed out of the object database.
	var files []int
	for i := range entries {
		if !entries[i].IsSymlink() {
			files = append(files, i)
		}
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
		for _, i := range files {
			if _, err := w.WriteString(entries[i].Blob + "\n"); err != nil {
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
		for _, i := range files {
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

	// Links are created after the regular files so their targets already exist,
	// which keeps the materialized tree free of transiently dangling links.
	for i := range entries {
		e := &entries[i]
		if !e.IsSymlink() {
			continue
		}
		full := filepath.Join(destDir, filepath.FromSlash(e.Path))
		if err := os.MkdirAll(filepath.Dir(full), 0755); err != nil {
			return nil, err
		}
		if err := os.Symlink(filepath.FromSlash(e.LinkTarget), full); err != nil {
			return nil, fmt.Errorf("create symlink %s: %w", e.Path, err)
		}
	}
	return entries, nil
}
