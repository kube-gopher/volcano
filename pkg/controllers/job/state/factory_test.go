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
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"

	"volcano.sh/volcano/pkg/controllers/apis"
)

// makeJobInfo constructs a minimal JobInfo with the given phase.
func makeJobInfo(phase vcbatch.JobPhase) *apis.JobInfo {
	return &apis.JobInfo{
		Job: &vcbatch.Job{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "test-job",
				Namespace: "default",
			},
			Status: vcbatch.JobStatus{
				State: vcbatch.JobState{Phase: phase},
			},
		},
	}
}

func TestNewState_KnownPhases(t *testing.T) {
	tests := []struct {
		name     string
		phase    vcbatch.JobPhase
		wantType reflect.Type
	}{
		{
			name:     "Pending phase returns pendingState",
			phase:    vcbatch.Pending,
			wantType: reflect.TypeOf(&pendingState{}),
		},
		{
			name:     "Running phase returns runningState",
			phase:    vcbatch.Running,
			wantType: reflect.TypeOf(&runningState{}),
		},
		{
			name:     "Restarting phase returns restartingState",
			phase:    vcbatch.Restarting,
			wantType: reflect.TypeOf(&restartingState{}),
		},
		{
			name:     "Terminating phase returns terminatingState",
			phase:    vcbatch.Terminating,
			wantType: reflect.TypeOf(&terminatingState{}),
		},
		{
			name:     "Aborting phase returns abortingState",
			phase:    vcbatch.Aborting,
			wantType: reflect.TypeOf(&abortingState{}),
		},
		{
			name:     "Aborted phase returns abortedState",
			phase:    vcbatch.Aborted,
			wantType: reflect.TypeOf(&abortedState{}),
		},
		{
			name:     "Completing phase returns completingState",
			phase:    vcbatch.Completing,
			wantType: reflect.TypeOf(&completingState{}),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NewState(makeJobInfo(tc.phase))
			if reflect.TypeOf(got) != tc.wantType {
				t.Errorf("NewState(%q) = %T, want %s", tc.phase, got, tc.wantType)
			}
		})
	}
}

// Terminated, Completed, and Failed all map to finishedState.
func TestNewState_FinishedPhases(t *testing.T) {
	finishedType := reflect.TypeOf(&finishedState{})
	phases := []vcbatch.JobPhase{
		vcbatch.Terminated,
		vcbatch.Completed,
		vcbatch.Failed,
	}
	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			got := NewState(makeJobInfo(phase))
			if reflect.TypeOf(got) != finishedType {
				t.Errorf("NewState(%q) = %T, want *finishedState", phase, got)
			}
		})
	}
}

// Unknown or empty phase falls back to pendingState.
func TestNewState_DefaultPhase(t *testing.T) {
	tests := []struct {
		name  string
		phase vcbatch.JobPhase
	}{
		{"empty string", ""},
		{"unknown phase", "Unknown"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NewState(makeJobInfo(tc.phase))
			if _, ok := got.(*pendingState); !ok {
				t.Errorf("NewState(%q) = %T, want *pendingState", tc.phase, got)
			}
		})
	}
}

// NewState must embed the same JobInfo pointer that was passed in.
func TestNewState_JobInfoBinding(t *testing.T) {
	tests := []struct {
		name      string
		phase     vcbatch.JobPhase
		checkFunc func(got State) *apis.JobInfo
	}{
		{
			name:  "pendingState binds jobInfo",
			phase: vcbatch.Pending,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*pendingState)
				return s.job
			},
		},
		{
			name:  "runningState binds jobInfo",
			phase: vcbatch.Running,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*runningState)
				return s.job
			},
		},
		{
			name:  "restartingState binds jobInfo",
			phase: vcbatch.Restarting,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*restartingState)
				return s.job
			},
		},
		{
			name:  "abortingState binds jobInfo",
			phase: vcbatch.Aborting,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*abortingState)
				return s.job
			},
		},
		{
			name:  "abortedState binds jobInfo",
			phase: vcbatch.Aborted,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*abortedState)
				return s.job
			},
		},
		{
			name:  "completingState binds jobInfo",
			phase: vcbatch.Completing,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*completingState)
				return s.job
			},
		},
		{
			name:  "terminatingState binds jobInfo",
			phase: vcbatch.Terminating,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*terminatingState)
				return s.job
			},
		},
		{
			name:  "finishedState binds jobInfo",
			phase: vcbatch.Completed,
			checkFunc: func(got State) *apis.JobInfo {
				s, _ := got.(*finishedState)
				return s.job
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			info := makeJobInfo(tc.phase)
			got := NewState(info)
			if bound := tc.checkFunc(got); bound != info {
				t.Errorf("state.job pointer mismatch: got %p, want %p", bound, info)
			}
		})
	}
}

// PodRetainPhaseNone must be an empty set — no phases are retained.
func TestNewState_PodRetainPhaseNone(t *testing.T) {
	if len(PodRetainPhaseNone) != 0 {
		t.Errorf("PodRetainPhaseNone should be empty, got %v", PodRetainPhaseNone)
	}
}

// PodRetainPhaseSoft must contain exactly PodSucceeded and PodFailed.
func TestNewState_PodRetainPhaseSoft(t *testing.T) {
	if _, ok := PodRetainPhaseSoft[v1.PodSucceeded]; !ok {
		t.Error("PodRetainPhaseSoft missing PodSucceeded")
	}
	if _, ok := PodRetainPhaseSoft[v1.PodFailed]; !ok {
		t.Error("PodRetainPhaseSoft missing PodFailed")
	}
	if len(PodRetainPhaseSoft) != 2 {
		t.Errorf("PodRetainPhaseSoft should have exactly 2 entries, got %d", len(PodRetainPhaseSoft))
	}
}
