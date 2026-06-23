package k8s

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
)

// RV (error-resilience HIGH): a broken runner pod must surface a clear reason
// instead of letting waitForPodReady poll silently forever. podStartupError is
// the fail-fast gate: it returns a descriptive error for unrecoverable startup
// states and nil while the pod is still legitimately coming up.
func TestPodStartupError(t *testing.T) {
	waiting := func(name, reason, msg string) *corev1.Pod {
		return &corev1.Pod{
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				ContainerStatuses: []corev1.ContainerStatus{{
					Name:  name,
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: msg}},
				}},
			},
		}
	}

	tests := []struct {
		name       string
		pod        *corev1.Pod
		wantErr    bool
		wantSubstr string
	}{
		{
			name:       "ImagePullBackOff fails fast with reason + message",
			pod:        waiting("runner", "ImagePullBackOff", `Back-off pulling image "registry.example/runner:latest"`),
			wantErr:    true,
			wantSubstr: "ImagePullBackOff",
		},
		{
			name:       "CrashLoopBackOff fails fast",
			pod:        waiting("runner", "CrashLoopBackOff", "back-off restarting failed container"),
			wantErr:    true,
			wantSubstr: "CrashLoopBackOff",
		},
		{
			name:       "CreateContainerConfigError fails fast (e.g. missing secret key)",
			pod:        waiting("runner", "CreateContainerConfigError", `secret "x" not found`),
			wantErr:    true,
			wantSubstr: "CreateContainerConfigError",
		},
		{
			name: "PodFailed phase surfaces its reason",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase: corev1.PodFailed, Reason: "Evicted", Message: "node out of memory",
			}},
			wantErr:    true,
			wantSubstr: "failed to start",
		},
		{
			name:    "ContainerCreating is still starting (no error)",
			pod:     waiting("runner", "ContainerCreating", ""),
			wantErr: false,
		},
		{
			name: "running/ready pod has no startup error",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase:             corev1.PodRunning,
				ContainerStatuses: []corev1.ContainerStatus{{Name: "runner", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}}},
			}},
			wantErr: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := podStartupError(tc.pod)
			if tc.wantErr && err == nil {
				t.Fatalf("expected an error, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if tc.wantErr && tc.wantSubstr != "" && !strings.Contains(err.Error(), tc.wantSubstr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantSubstr)
			}
		})
	}
}
