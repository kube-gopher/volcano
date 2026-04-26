/*
Copyright 2024 The Volcano Authors.

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

	"volcano.sh/volcano/pkg/controllers/apis"
)

// killJobCall records one invocation of the KillJob stub.
type killJobCall struct {
	job             *apis.JobInfo
	podRetainPhase  PhaseMap
	updateFnIsNil   bool
}

// stubKillJob replaces the KillJob package variable with a stub that records
// its arguments and returns the given error. It restores the original value
// via t.Cleanup so parallel tests are not affected.
func stubKillJob(t *testing.T, returnErr error) *killJobCall {
	t.Helper()
	original := KillJob
	call := &killJobCall{}
	KillJob = func(job *apis.JobInfo, podRetainPhase PhaseMap, fn UpdateStatusFn) error {
		call.job = job
		call.podRetainPhase = podRetainPhase
		call.updateFnIsNil = fn == nil
		return returnErr
	}
	t.Cleanup(func() { KillJob = original })
	return call
}

// TestFinishedState_Execute_ActionAgnostic verifies that Execute always
// delegates to KillJob regardless of the action type.
func TestFinishedState_Execute_ActionAgnostic(t *testing.T) {
	actions := []struct {
		name   string
		action v1alpha1.Action
	}{
		{"SyncJobAction", v1alpha1.SyncJobAction},
		{"RestartJobAction", v1alpha1.RestartJobAction},
		{"AbortJobAction", v1alpha1.AbortJobAction},
		{"TerminateJobAction", v1alpha1.TerminateJobAction},
		{"CompleteJobAction", v1alpha1.CompleteJobAction},
		{"ResumeJobAction", v1alpha1.ResumeJobAction},
		{"RestartTaskAction", v1alpha1.RestartTaskAction},
		{"RestartPodAction", v1alpha1.RestartPodAction},
		{"RestartPartitionAction", v1alpha1.RestartPartitionAction},
		{"UnknownAction", v1alpha1.Action("Unknown")},
	}

	for _, tc := range actions {
		t.Run(tc.name, func(t *testing.T) {
			call := stubKillJob(t, nil)
			info := makeJobInfo(vcbatch.Completed)
			s := &finishedState{job: info}

			if err := s.Execute(Action{Action: tc.action}); err != nil {
				t.Fatalf("Execute(%q) returned unexpected error: %v", tc.action, err)
			}
			if call.job == nil {
				t.Fatal("KillJob was not called")
			}
		})
	}
}

// TestFinishedState_Execute_UsesSoftRetainPhase verifies that Execute passes
// PodRetainPhaseSoft (Succeeded + Failed retained) rather than
// PodRetainPhaseNone (all pods killed).
func TestFinishedState_Execute_UsesSoftRetainPhase(t *testing.T) {
	call := stubKillJob(t, nil)
	s := &finishedState{job: makeJobInfo(vcbatch.Completed)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	for _, phase := range []v1.PodPhase{v1.PodSucceeded, v1.PodFailed} {
		if _, ok := call.podRetainPhase[phase]; !ok {
			t.Errorf("podRetainPhase missing %q; got %v", phase, call.podRetainPhase)
		}
	}
	if len(call.podRetainPhase) != 2 {
		t.Errorf("podRetainPhase should have exactly 2 entries, got %d", len(call.podRetainPhase))
	}
}

// TestFinishedState_Execute_NilUpdateFn verifies that Execute passes a nil
// update function — the finished state must not change job phase.
func TestFinishedState_Execute_NilUpdateFn(t *testing.T) {
	call := stubKillJob(t, nil)
	s := &finishedState{job: makeJobInfo(vcbatch.Completed)}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !call.updateFnIsNil {
		t.Error("KillJob updateFn should be nil for finishedState")
	}
}

// TestFinishedState_Execute_PassesJobInfo verifies that Execute forwards the
// correct JobInfo pointer to KillJob.
func TestFinishedState_Execute_PassesJobInfo(t *testing.T) {
	call := stubKillJob(t, nil)
	info := makeJobInfo(vcbatch.Terminated)
	s := &finishedState{job: info}

	if err := s.Execute(Action{Action: v1alpha1.SyncJobAction}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if call.job != info {
		t.Errorf("KillJob received wrong JobInfo: got %p, want %p", call.job, info)
	}
}

// TestFinishedState_Execute_PropagatesError verifies that any error returned
// by KillJob is surfaced to the caller unchanged.
func TestFinishedState_Execute_PropagatesError(t *testing.T) {
	want := errors.New("kill failed")
	stubKillJob(t, want)
	s := &finishedState{job: makeJobInfo(vcbatch.Failed)}

	if got := s.Execute(Action{Action: v1alpha1.SyncJobAction}); !errors.Is(got, want) {
		t.Errorf("Execute returned %v, want %v", got, want)
	}
}
