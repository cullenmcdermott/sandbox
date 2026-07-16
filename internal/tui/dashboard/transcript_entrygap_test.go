package dashboard

import "testing"

// TestStartsEntryPredicate locks the D2 entry-gap boundary rule: which block
// kinds open a new top-level entry (earning a leading blank) relative to the
// block before them. User/assistant/reasoning/subagent/todos always open one; a
// tool card opens one only when it doesn't directly follow another tool card;
// info/error/shell/footer fold into the entry above them.
func TestStartsEntryPredicate(t *testing.T) {
	cases := []struct {
		name string
		prev tblockKind
		cur  tblockKind
		want bool
	}{
		{"user after assistant", blockAssistant, blockUser, true},
		{"assistant after user", blockUser, blockAssistant, true},
		{"reasoning after assistant", blockAssistant, blockReasoning, true},
		{"subagent after assistant", blockAssistant, blockSubagent, true},
		{"todos after assistant", blockAssistant, blockTodos, true},
		{"first tool card after assistant", blockAssistant, blockToolCard, true},
		{"tool card after tool card is tight", blockToolCard, blockToolCard, false},
		{"info attaches", blockAssistant, blockInfo, false},
		{"error attaches", blockToolCard, blockError, false},
		{"shell attaches", blockAssistant, blockShell, false},
		{"footer attaches", blockAssistant, blockFooter, false},
	}
	for _, c := range cases {
		if got := startsEntry(c.prev, c.cur); got != c.want {
			t.Errorf("%s: startsEntry(%v,%v)=%v, want %v", c.name, c.prev, c.cur, got, c.want)
		}
	}
}

// TestEntryGapCommitFlags drives a representative committed sequence through
// commitItems and asserts the per-block entryGap flag: the first block never
// gets one, consecutive tool cards stay tight, and info/footer attach without a
// gap while every other entry kind opens one.
func TestEntryGapCommitFlags(t *testing.T) {
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	kinds := []tblockKind{
		blockUser,      // 0: first — no gap
		blockAssistant, // 1: new entry — gap
		blockToolCard,  // 2: new entry (prev not tool) — gap
		blockToolCard,  // 3: consecutive tool — tight
		blockInfo,      // 4: attaches — no gap
		blockFooter,    // 5: attaches — no gap
		blockReasoning, // 6: new entry — gap
	}
	for _, k := range kinds {
		m.blocks = append(m.blocks, m.newBlockCard(k, "x"))
	}
	m.commitItems()

	want := []bool{false, true, true, false, false, false, true}
	for i, b := range m.blocks {
		if b.entryGap != want[i] {
			t.Errorf("block %d (%v): entryGap=%v, want %v", i, b.kind, b.entryGap, want[i])
		}
	}
}

// TestStreamTailEntryGapMatchesCommitted is the T1 parity guard for the entry
// gap: the ephemeral streaming tail (assistant or reasoning) must carry the SAME
// leading blank the committed block will get at commit time, or the frame height
// jumps by a row at the commit boundary. With a prior user block, both the
// assistant tail and the reasoning tail open a new entry (gap == true).
func TestStreamTailEntryGapMatchesCommitted(t *testing.T) {
	// Assistant tail after a committed user block.
	m := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m.width, m.height = 80, 200
	m.blocks = append(m.blocks, m.newBlockCard(blockUser, "hi"))
	m.assistantBuf.WriteString("streaming reply")
	m.streaming = true
	m.commitItems()
	if m.streamItem == nil {
		t.Fatal("assistant stream tail not created")
	}
	committed := startsEntry(blockUser, blockAssistant)
	if m.streamItem.entryGap != committed {
		t.Errorf("assistant tail entryGap=%v, want %v (== committed block's gap)", m.streamItem.entryGap, committed)
	}

	// Reasoning tail after a committed user block.
	m2 := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m2.width, m2.height = 80, 200
	m2.blocks = append(m2.blocks, m2.newBlockCard(blockUser, "hi"))
	m2.reasoning = true
	m2.reasoningBuf.WriteString("thinking…")
	m2.commitItems()
	if m2.streamItem == nil {
		t.Fatal("reasoning stream tail not created")
	}
	committedR := startsEntry(blockUser, blockReasoning)
	if m2.streamItem.entryGap != committedR {
		t.Errorf("reasoning tail entryGap=%v, want %v (== committed block's gap)", m2.streamItem.entryGap, committedR)
	}

	// A tail with no prior committed block opens the transcript — no leading gap.
	m3 := NewTranscript(&fakeRunnerClient{}, transcriptSession(), nil)
	m3.width, m3.height = 80, 200
	m3.assistantBuf.WriteString("first")
	m3.streaming = true
	m3.commitItems()
	if m3.streamItem == nil {
		t.Fatal("first-entry stream tail not created")
	}
	if m3.streamItem.entryGap {
		t.Error("stream tail as first entry must not carry a leading gap")
	}
}
