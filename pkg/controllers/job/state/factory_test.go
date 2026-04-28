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
	"reflect"
	"testing"

	v1 "k8s.io/api/core/v1"

	vcbatch "volcano.sh/apis/pkg/apis/batch/v1alpha1"

	"volcano.sh/volcano/pkg/controllers/apis"
)

func extractJobInfo(s State) *apis.JobInfo {
	switch v := s.(type) {
	case *pendingState:
		return v.job
	case *runningState:
		return v.job
	case *restartingState:
		return v.job
	case *abortingState:
		return v.job
	case *abortedState:
		return v.job
	case *completingState:
		return v.job
	case *terminatingState:
		return v.job
	case *finishedState:
		return v.job
	}
	return nil
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

func TestNewState_JobInfoBinding(t *testing.T) {
	phases := []vcbatch.JobPhase{
		vcbatch.Pending,
		vcbatch.Running,
		vcbatch.Restarting,
		vcbatch.Aborting,
		vcbatch.Aborted,
		vcbatch.Completing,
		vcbatch.Terminating,
		vcbatch.Completed,
	}
	for _, phase := range phases {
		t.Run(string(phase), func(t *testing.T) {
			info := makeJobInfo(phase)
			got := NewState(info)
			bound := extractJobInfo(got)
			if bound == nil {
				t.Fatalf("extractJobInfo returned nil for state %T", got)
			}
			if bound != info {
				t.Errorf("state.job pointer mismatch: got %p, want %p", bound, info)
			}
		})
	}
}

func TestNewState_PodRetainPhaseNone(t *testing.T) {
	if len(PodRetainPhaseNone) != 0 {
		t.Errorf("PodRetainPhaseNone should be empty, got %v", PodRetainPhaseNone)
	}
}

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
