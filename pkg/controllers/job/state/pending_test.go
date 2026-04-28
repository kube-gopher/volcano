/*
Copyright 2017 The Volcano Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package state

import (
	"errors"
	"testing"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"
	"volcano.sh/apis/pkg/apis/bus/v1alpha1"
)

func TestPendingState_Execute_RestartJobCallsKillJob(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("KillJob was not called")
	}
}

// RestartJobAction uses PodRetainPhaseNone (kills completed pods too) — unlike
// Abort/Complete/Terminate which use Soft.
func TestPendingState_Execute_RestartJobUsesRetainPhaseNone(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.podRetainPhase) != 0 {
		t.Errorf("podRetainPhase should be empty (PhaseNone), got %v", c.podRetainPhase)
	}
}

func TestPendingState_Execute_RestartJobUpdateFn(t *testing.T) {
	tests := []struct {
		name         string
		initialRetry int32
		wantRetry    int32
	}{
		{"from zero", 0, 1},
		{"from non-zero", 2, 3},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{
				RetryCount: tc.initialRetry,
				State:      vcbatch.JobState{Phase: vcbatch.Pending},
			}
			changed := c.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != vcbatch.Restarting {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Restarting)
			}
			if status.RetryCount != tc.wantRetry {
				t.Errorf("RetryCount = %d, want %d", status.RetryCount, tc.wantRetry)
			}
		})
	}
}

func TestPendingState_Execute_RestartJobPassesJobInfo(t *testing.T) {
	c := captureKillJob(t, nil)
	info := makeJobInfo(vcbatch.Pending)
	s := &pendingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

func TestPendingState_Execute_RestartJobPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if got := s.Execute(Action{Action: v1alpha1.RestartJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

func TestPendingState_Execute_RestartTargetActionsCallKillTarget(t *testing.T) {
	actions := []struct {
		name   string
		action v1alpha1.Action
	}{
		{"RestartTaskAction", v1alpha1.RestartTaskAction},
		{"RestartPodAction", v1alpha1.RestartPodAction},
		{"RestartPartitionAction", v1alpha1.RestartPartitionAction},
	}
	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillTarget(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if c.job == nil {
				t.Fatal("KillTarget was not called")
			}
		})
	}
}

func TestPendingState_Execute_RestartTargetForwardsTarget(t *testing.T) {
	tests := []struct {
		name   string
		action v1alpha1.Action
		target Target
	}{
		{
			name:   "task target",
			action: v1alpha1.RestartTaskAction,
			target: Target{TaskName: "worker", Type: TargetTypeTask},
		},
		{
			name:   "pod target",
			action: v1alpha1.RestartPodAction,
			target: Target{TaskName: "worker", PodName: "worker-0", Type: TargetTypePod},
		},
		{
			name:   "partition target",
			action: v1alpha1.RestartPartitionAction,
			target: Target{TaskName: "worker", PodName: "worker-0", PartitionName: "p0", Type: TargetTypePartition},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureKillTarget(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: tc.action, Target: tc.target}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.target != tc.target {
				t.Errorf("KillTarget received target %+v, want %+v", c.target, tc.target)
			}
		})
	}
}

func TestPendingState_Execute_RestartTargetUpdateFn(t *testing.T) {
	c := captureKillTarget(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 1, State: vcbatch.JobState{Phase: vcbatch.Pending}}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Restarting {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Restarting)
	}
	if status.RetryCount != 2 {
		t.Errorf("RetryCount = %d, want 2", status.RetryCount)
	}
}

func TestPendingState_Execute_RestartTargetPropagatesError(t *testing.T) {
	want := errors.New("kill target failed")
	captureKillTarget(t, want)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if got := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

func TestPendingState_Execute_KillJobBranchesUsesSoftRetainPhase(t *testing.T) {
	actions := []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.CompleteJobAction,
		v1alpha1.TerminateJobAction,
	}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: action}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for phase := range PodRetainPhaseSoft {
				if _, ok := c.podRetainPhase[phase]; !ok {
					t.Errorf("podRetainPhase missing %q", phase)
				}
			}
			if len(c.podRetainPhase) != len(PodRetainPhaseSoft) {
				t.Errorf("podRetainPhase has %d entries, want %d", len(c.podRetainPhase), len(PodRetainPhaseSoft))
			}
		})
	}
}

func TestPendingState_Execute_KillJobBranchesUpdateFn(t *testing.T) {
	tests := []struct {
		action    v1alpha1.Action
		wantPhase vcbatch.JobPhase
	}{
		{v1alpha1.AbortJobAction, vcbatch.Aborting},
		{v1alpha1.CompleteJobAction, vcbatch.Completing},
		{v1alpha1.TerminateJobAction, vcbatch.Terminating},
	}
	for _, tc := range tests {
		t.Run(string(tc.action), func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Pending}}
			changed := c.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", status.State.Phase, tc.wantPhase)
			}
		})
	}
}

func TestPendingState_Execute_KillJobBranchesPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	actions := []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.CompleteJobAction,
		v1alpha1.TerminateJobAction,
	}
	for _, action := range actions {
		t.Run(string(action), func(t *testing.T) {
			captureKillJob(t, want)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

			if got := s.Execute(Action{Action: action}); !errors.Is(got, want) {
				t.Errorf("Execute returned %v, want %v", got, want)
			}
		})
	}
}

func TestPendingState_Execute_DefaultCallsSyncJob(t *testing.T) {
	c := captureSyncJob(t, nil)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("SyncJob was not called")
	}
}

func TestPendingState_Execute_DefaultPassesJobInfo(t *testing.T) {
	c := captureSyncJob(t, nil)
	info := makeJobInfo(vcbatch.Pending, withMinAvailable(1))
	s := &pendingState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job != info {
		t.Errorf("SyncJob received wrong JobInfo: got %p, want %p", c.job, info)
	}
}

func TestPendingState_Execute_DefaultUpdateFnTransitionsToRunning(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		status       vcbatch.JobStatus
	}{
		{
			name:         "sum equals MinAvailable",
			minAvailable: 3,
			status:       vcbatch.JobStatus{Running: 3},
		},
		{
			name:         "sum exceeds MinAvailable",
			minAvailable: 2,
			status:       vcbatch.JobStatus{Running: 1, Succeeded: 1, Failed: 1},
		},
		{
			name:         "only Succeeded meets MinAvailable",
			minAvailable: 2,
			status:       vcbatch.JobStatus{Succeeded: 2},
		},
		{
			name:         "only Failed meets MinAvailable",
			minAvailable: 1,
			status:       vcbatch.JobStatus{Failed: 1},
		},
		{
			name:         "MinAvailable is zero",
			minAvailable: 0,
			status:       vcbatch.JobStatus{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending, withMinAvailable(tc.minAvailable))}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			changed := c.updateFn(&tc.status)

			if !changed {
				t.Error("updateFn should return true when MinAvailable is met")
			}
			if tc.status.State.Phase != vcbatch.Running {
				t.Errorf("phase = %q, want %q", tc.status.State.Phase, vcbatch.Running)
			}
		})
	}
}

func TestPendingState_Execute_DefaultUpdateFnStaysPending(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		status       vcbatch.JobStatus
	}{
		{
			name:         "no pods at all",
			minAvailable: 1,
			status:       vcbatch.JobStatus{},
		},
		{
			name:         "sum one short of MinAvailable",
			minAvailable: 3,
			status:       vcbatch.JobStatus{Running: 1, Succeeded: 1},
		},
		{
			// Pending pods do not count toward MinAvailable.
			name:         "only Pending pods, not counted",
			minAvailable: 2,
			status:       vcbatch.JobStatus{Pending: 5, Running: 0},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			s := &pendingState{job: makeJobInfo(vcbatch.Pending, withMinAvailable(tc.minAvailable))}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			originalPhase := tc.status.State.Phase
			changed := c.updateFn(&tc.status)

			if changed {
				t.Error("updateFn should return false when MinAvailable is not met")
			}
			if tc.status.State.Phase != originalPhase {
				t.Errorf("phase should not change: got %q, want %q", tc.status.State.Phase, originalPhase)
			}
		})
	}
}

func TestPendingState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("sync failed")
	captureSyncJob(t, want)
	s := &pendingState{job: makeJobInfo(vcbatch.Pending)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
