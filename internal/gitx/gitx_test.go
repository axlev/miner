package gitx

import (
	"testing"
)

func TestExtractHunkSymbols(t *testing.T) {
	diffText := `
diff --git a/bgpd/bgp_fsm.c b/bgpd/bgp_fsm.c
index 1234567..89abcdef 100644
--- a/bgpd/bgp_fsm.c
+++ b/bgpd/bgp_fsm.c
@@ -1020,7 +1020,10 @@ int bgp_fsm_change_status(struct peer *peer, int status)
 {
 	int old_status = peer->status;
 
+	if (status == Established) {
+		bgp_timer_set(peer);
+	}
 	return 0;
 }
@@ -1500,6 +1503,14 @@ static void bgp_stop(struct peer *peer)
 {
+	peer_reset_state(peer);
+}
+
+void bgp_peer_graceful_restart(struct peer *peer)
+{
+	peer->gr_timer = 1;
+}
`

	symbols := ExtractHunkSymbols(diffText)

	expected := map[string]bool{
		"bgp_fsm_change_status":        true,
		"bgp_stop":                     true,
		"bgp_peer_graceful_restart":   true,
	}

	for _, s := range symbols {
		if !expected[s] {
			t.Errorf("unexpected symbol extracted: %s", s)
		}
		delete(expected, s)
	}

	if len(expected) > 0 {
		t.Errorf("missing expected symbols: %+v", expected)
	}
}

func TestParseNumstat(t *testing.T) {
	numstatOutput := `
15	4	bgpd/bgp_fsm.c
0	20	zebra/zebra_rib.c
45	0	lib/event.c
`
	files := ParseNumstat(numstatOutput)
	if len(files) != 3 {
		t.Fatalf("expected 3 files, got %d", len(files))
	}

	if files[0].Path != "bgpd/bgp_fsm.c" || files[0].Additions != 15 || files[0].Deletions != 4 || files[0].Status != "modified" {
		t.Errorf("unexpected file 0: %+v", files[0])
	}

	if files[1].Path != "zebra/zebra_rib.c" || files[1].Status != "deleted" {
		t.Errorf("unexpected file 1: %+v", files[1])
	}

	if files[2].Path != "lib/event.c" || files[2].Status != "added" {
		t.Errorf("unexpected file 2: %+v", files[2])
	}
}
