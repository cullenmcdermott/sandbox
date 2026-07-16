package dashboard

import "testing"

func TestLeaderStep(t *testing.T) {
	tests := []struct {
		name  string
		armed bool
		key   string
		want  leaderAction
	}{
		// Not armed: only the leader keys arm; everything else forwards normally.
		{"unarmed ctrl+] arms", false, "ctrl+]", leaderArm},
		{"unarmed ctrl+4 arms", false, "ctrl+4", leaderArm},
		{"unarmed g ignored", false, "g", leaderIgnore},
		{"unarmed k ignored", false, "k", leaderIgnore},
		{"unarmed x ignored", false, "x", leaderIgnore},
		{"unarmed esc ignored", false, "esc", leaderIgnore},
		{"unarmed enter ignored", false, "enter", leaderIgnore},

		// Armed: leader keys detach, g/k jump, anything else forwards.
		{"armed ctrl+] detaches", true, "ctrl+]", leaderDetach},
		{"armed ctrl+4 detaches", true, "ctrl+4", leaderDetach},
		{"armed g jumps next", true, "g", leaderJumpNext},
		{"armed k jumps prev", true, "k", leaderJumpPrev},
		{"armed x forwards", true, "x", leaderForward},
		{"armed esc forwards", true, "esc", leaderForward},
		{"armed enter forwards", true, "enter", leaderForward},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := leaderStep(tt.armed, tt.key); got != tt.want {
				t.Fatalf("leaderStep(%v, %q) = %d, want %d", tt.armed, tt.key, got, tt.want)
			}
		})
	}
}
