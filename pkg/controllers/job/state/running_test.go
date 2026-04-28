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

	v1 "k8s.io/api/core/v1"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"
	"volcano.sh/apis/pkg/apis/bus/v1alpha1"
)

func taskStatus(succeeded int32) vcbatch.TaskState {
	return vcbatch.TaskState{
		Phase: map[v1.PodPhase]int32{v1.PodSucceeded: succeeded},
	}
}

func TestRunningState_Execute_RestartJobCallsKillJob(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("KillJob was not called")
	}
}

func TestRunningState_Execute_RestartJobUsesRetainPhaseNone(t *testing.T) {
	c := captureKillJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(c.podRetainPhase) != 0 {
		t.Errorf("podRetainPhase should be empty (PhaseNone), got %v", c.podRetainPhase)
	}
}

func TestRunningState_Execute_RestartJobUpdateFn(t *testing.T) {
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
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: v1alpha1.RestartJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{
				RetryCount: tc.initialRetry,
				State:      vcbatch.JobState{Phase: vcbatch.Running},
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

func TestRunningState_Execute_RestartJobPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	captureKillJob(t, want)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if got := s.Execute(Action{Action: v1alpha1.RestartJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

func TestRunningState_Execute_RestartTargetActionsCallKillTarget(t *testing.T) {
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
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.job == nil {
				t.Fatal("KillTarget was not called")
			}
		})
	}
}

func TestRunningState_Execute_RestartTargetForwardsTarget(t *testing.T) {
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
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: tc.action, Target: tc.target}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.target != tc.target {
				t.Errorf("KillTarget received target %+v, want %+v", c.target, tc.target)
			}
		})
	}
}

func TestRunningState_Execute_RestartTargetUpdateFn(t *testing.T) {
	c := captureKillTarget(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.updateFn == nil {
		t.Fatal("expected non-nil updateFn")
	}

	status := &vcbatch.JobStatus{RetryCount: 1, State: vcbatch.JobState{Phase: vcbatch.Running}}
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

func TestRunningState_Execute_RestartTargetPropagatesError(t *testing.T) {
	want := errors.New("kill target failed")
	captureKillTarget(t, want)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if got := s.Execute(Action{Action: v1alpha1.RestartTaskAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

func TestRunningState_Execute_KillJobBranchesUsesSoftRetainPhase(t *testing.T) {
	for _, action := range []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.TerminateJobAction,
		v1alpha1.CompleteJobAction,
	} {
		t.Run(string(action), func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

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

func TestRunningState_Execute_KillJobBranchesUpdateFn(t *testing.T) {
	tests := []struct {
		action    v1alpha1.Action
		wantPhase vcbatch.JobPhase
	}{
		{v1alpha1.AbortJobAction, vcbatch.Aborting},
		{v1alpha1.TerminateJobAction, vcbatch.Terminating},
		{v1alpha1.CompleteJobAction, vcbatch.Completing},
	}
	for _, tc := range tests {
		t.Run(string(tc.action), func(t *testing.T) {
			c := captureKillJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if c.updateFn == nil {
				t.Fatal("expected non-nil updateFn")
			}

			status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Running}}
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

func TestRunningState_Execute_KillJobBranchesPropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	for _, action := range []v1alpha1.Action{
		v1alpha1.AbortJobAction,
		v1alpha1.TerminateJobAction,
		v1alpha1.CompleteJobAction,
	} {
		t.Run(string(action), func(t *testing.T) {
			captureKillJob(t, want)
			s := &runningState{job: makeJobInfo(vcbatch.Running)}

			if got := s.Execute(Action{Action: action}); !errors.Is(got, want) {
				t.Errorf("Execute returned %v, want %v", got, want)
			}
		})
	}
}

func TestRunningState_Execute_DefaultCallsSyncJob(t *testing.T) {
	c := captureSyncJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if c.job == nil {
		t.Fatal("SyncJob was not called")
	}
}

func TestRunningState_Execute_DefaultPropagatesError(t *testing.T) {
	want := errors.New("sync failed")
	captureSyncJob(t, want)
	s := &runningState{job: makeJobInfo(vcbatch.Running)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}

func TestRunningState_Execute_DefaultUpdateFnScaleToZero(t *testing.T) {
	c := captureSyncJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running, withTasks(vcbatch.TaskSpec{Replicas: 0}))}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{State: vcbatch.JobState{Phase: vcbatch.Running}}
	changed := c.updateFn(status)

	if changed {
		t.Error("updateFn should return false for scale-to-zero")
	}
	if status.State.Phase != vcbatch.Running {
		t.Errorf("phase should stay Running, got %q", status.State.Phase)
	}
}

// MinSuccess satisfaction must short-circuit before the all-terminal check.
func TestRunningState_Execute_DefaultUpdateFnMinSuccessEarlyExit(t *testing.T) {
	tests := []struct {
		name      string
		minSuc    int32
		succeeded int32
	}{
		{"exactly meets minSuccess", 3, 3},
		{"exceeds minSuccess", 3, 5},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running,
				withMinAvailable(1),
				withMinSuccess(int32Ptr(tc.minSuc)),
				withTasks(vcbatch.TaskSpec{Replicas: 5}))}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := &vcbatch.JobStatus{Succeeded: tc.succeeded, Running: 2}
			changed := c.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != vcbatch.Completed {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Completed)
			}
		})
	}
}

func TestRunningState_Execute_DefaultUpdateFnPerTaskCheckFails(t *testing.T) {
	// MinAvailable(3) >= totalTaskMinAvailable(2) so the per-task loop runs;
	// "worker" needs 2 succeeded but only got 1 → Failed.
	c := captureSyncJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running,
		withMinAvailable(3),
		withTasks(vcbatch.TaskSpec{Name: "worker", Replicas: 3, MinAvailable: int32Ptr(2)}))}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{
		Succeeded: 1,
		Failed:    2,
		TaskStatusCount: map[string]vcbatch.TaskState{
			"worker": taskStatus(1),
		},
	}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Failed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Failed)
	}
}

func TestRunningState_Execute_DefaultUpdateFnPerTaskCheckSkipped(t *testing.T) {
	// MinAvailable(1) < totalTaskMinAvailable(3) so per-task loop is skipped
	// and the all-terminal branch decides the outcome.
	c := captureSyncJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running,
		withMinAvailable(1),
		withTasks(vcbatch.TaskSpec{Name: "worker", Replicas: 3, MinAvailable: int32Ptr(3)}))}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{
		Succeeded: 3,
		Failed:    0,
		TaskStatusCount: map[string]vcbatch.TaskState{
			"worker": taskStatus(3),
		},
	}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Completed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Completed)
	}
}

// A task with MinAvailable but missing from TaskStatusCount must not block
// completion (TaskStatusCount lookup ok=false skips the inner check).
func TestRunningState_Execute_DefaultUpdateFnPerTaskNoStatus(t *testing.T) {
	c := captureSyncJob(t, nil)
	s := &runningState{job: makeJobInfo(vcbatch.Running,
		withMinAvailable(2),
		withTasks(vcbatch.TaskSpec{Name: "worker", Replicas: 2, MinAvailable: int32Ptr(2)}))}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	status := &vcbatch.JobStatus{
		Succeeded:       2,
		Failed:          0,
		TaskStatusCount: map[string]vcbatch.TaskState{},
	}
	changed := c.updateFn(status)

	if !changed {
		t.Error("updateFn should return true")
	}
	if status.State.Phase != vcbatch.Completed {
		t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Completed)
	}
}

func TestRunningState_Execute_DefaultUpdateFnAllTerminalOutcomes(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		minSuccess   *int32
		status       vcbatch.JobStatus
		wantPhase    vcbatch.JobPhase
	}{
		{
			name:         "minSuccess not met → Failed",
			minAvailable: 1,
			minSuccess:   int32Ptr(4),
			status:       vcbatch.JobStatus{Succeeded: 2, Failed: 1},
			wantPhase:    vcbatch.Failed,
		},
		{
			name:         "Succeeded meets MinAvailable → Completed",
			minAvailable: 2,
			minSuccess:   nil,
			status:       vcbatch.JobStatus{Succeeded: 2, Failed: 1},
			wantPhase:    vcbatch.Completed,
		},
		{
			name:         "Succeeded below MinAvailable → Failed",
			minAvailable: 3,
			minSuccess:   nil,
			status:       vcbatch.JobStatus{Succeeded: 1, Failed: 2},
			wantPhase:    vcbatch.Failed,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			// No per-task MinAvailable so the per-task loop is a no-op and
			// only the all-terminal branch decides the phase.
			s := &runningState{job: makeJobInfo(vcbatch.Running,
				withMinAvailable(tc.minAvailable),
				withMinSuccess(tc.minSuccess),
				withTasks(vcbatch.TaskSpec{Replicas: 3}))}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			changed := c.updateFn(&tc.status)

			if !changed {
				t.Error("updateFn should return true when all pods are terminal")
			}
			if tc.status.State.Phase != tc.wantPhase {
				t.Errorf("phase = %q, want %q", tc.status.State.Phase, tc.wantPhase)
			}
		})
	}
}

func TestRunningState_Execute_DefaultUpdateFnTooManyPending(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		taskReplicas int32
		pending      int32
	}{
		{
			// threshold = 4-2=2; Pending=3 > 2 → Pending
			name:         "pending exceeds threshold",
			minAvailable: 2,
			taskReplicas: 4,
			pending:      3,
		},
		{
			// threshold = 5-1=4; Pending=5 > 4 → Pending
			name:         "all pods pending",
			minAvailable: 1,
			taskReplicas: 5,
			pending:      5,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running,
				withMinAvailable(tc.minAvailable),
				withTasks(vcbatch.TaskSpec{Replicas: tc.taskReplicas}))}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := &vcbatch.JobStatus{Pending: tc.pending}
			changed := c.updateFn(status)

			if !changed {
				t.Error("updateFn should return true")
			}
			if status.State.Phase != vcbatch.Pending {
				t.Errorf("phase = %q, want %q", status.State.Phase, vcbatch.Pending)
			}
		})
	}
}

func TestRunningState_Execute_DefaultUpdateFnKeepsRunning(t *testing.T) {
	tests := []struct {
		name         string
		minAvailable int32
		taskReplicas int32
		status       vcbatch.JobStatus
	}{
		{
			name:         "pods still running",
			minAvailable: 2,
			taskReplicas: 4,
			status:       vcbatch.JobStatus{Running: 2, Pending: 2},
		},
		{
			// threshold = 4-2=2; Pending=2 is NOT > 2 → no transition.
			name:         "pending at threshold boundary",
			minAvailable: 2,
			taskReplicas: 4,
			status:       vcbatch.JobStatus{Pending: 2, Running: 2},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := captureSyncJob(t, nil)
			s := &runningState{job: makeJobInfo(vcbatch.Running,
				withMinAvailable(tc.minAvailable),
				withTasks(vcbatch.TaskSpec{Replicas: tc.taskReplicas}))}

			if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			status := tc.status
			originalPhase := status.State.Phase
			changed := c.updateFn(&status)

			if changed {
				t.Error("updateFn should return false when no terminal condition met")
			}
			if status.State.Phase != originalPhase {
				t.Errorf("phase should remain %q, got %q", originalPhase, status.State.Phase)
			}
		})
	}
}
