/*
Copyright 2025.

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

package controller

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	batchopsv1alpha1 "github.com/matanryngler/parallax/api/v1alpha1"
)

// TestListCronJobController_BasicSetup verifies that the ListCronJobReconciler
// can be properly initialized with a client and scheme.
func TestListCronJobController_BasicSetup(t *testing.T) {
	scheme := runtime.NewScheme()
	err := batchopsv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)
	err = corev1.AddToScheme(scheme)
	require.NoError(t, err)
	err = batchv1.AddToScheme(scheme)
	require.NoError(t, err)

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	// Test that the reconciler is properly configured
	assert.NotNil(t, reconciler.Client)
	assert.NotNil(t, reconciler.Scheme)
}

// TestListCronJobController_CronJobCreation verifies that a ListCronJob resource
// can be created with all required specifications including schedule, concurrency policy,
// and job history limits.
func TestListCronJobController_CronJobCreation(t *testing.T) {
	scheme := runtime.NewScheme()
	err := batchopsv1alpha1.AddToScheme(scheme)
	require.NoError(t, err)
	err = corev1.AddToScheme(scheme)
	require.NoError(t, err)
	err = batchv1.AddToScheme(scheme)
	require.NoError(t, err)

	// Create a ConfigMap with static list data
	configMap := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-source",
			Namespace: "default",
		},
		Data: map[string]string{
			"items": "item1\nitem2\nitem3",
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(configMap).
		Build()

	// Create a ListCronJob
	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			ListSourceRef: "test-source",
			Parallelism:   3,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo", "Processing item: $ITEM"},
				EnvName: "ITEM",
			},
			Schedule:                   "0 0 * * *",
			ConcurrencyPolicy:          batchv1.ForbidConcurrent,
			SuccessfulJobsHistoryLimit: func() *int32 { i := int32(3); return &i }(),
			FailedJobsHistoryLimit:     func() *int32 { i := int32(1); return &i }(),
		},
	}

	err = fakeClient.Create(context.Background(), listCronJob)
	require.NoError(t, err)

	// Test basic spec validation
	assert.Equal(t, "test-source", listCronJob.Spec.ListSourceRef)
	assert.Equal(t, int32(3), listCronJob.Spec.Parallelism)
	assert.Equal(t, "busybox", listCronJob.Spec.Template.Image)
	assert.Equal(t, "0 0 * * *", listCronJob.Spec.Schedule)
	assert.Equal(t, batchv1.ForbidConcurrent, listCronJob.Spec.ConcurrencyPolicy)
	assert.Equal(t, int32(3), *listCronJob.Spec.SuccessfulJobsHistoryLimit)
	assert.Equal(t, int32(1), *listCronJob.Spec.FailedJobsHistoryLimit)
}

// TestTrackCronJobCycles_NewCycleDetected verifies that when a CronJob's LastScheduleTime
// changes (indicating a new cycle has started), the ListCronJob status is updated to track
// this new schedule time. This ensures we can detect and log new cycle starts.
func TestTrackCronJobCycles_NewCycleDetected(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	scheduleTime := metav1.Now()

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			Schedule:          "*/1 * * * *",
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			StaticList:        []string{"item1", "item2"},
			Parallelism:       1,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo", "test"},
			},
		},
		Status: batchopsv1alpha1.ListCronJobStatus{
			// No LastScheduleTime set initially
		},
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: &scheduleTime,
			Active:           []corev1.ObjectReference{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listCronJob).
		WithStatusSubresource(listCronJob).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	err := reconciler.trackCronJobCycles(ctx, listCronJob, cronJob)
	require.NoError(t, err)

	// Verify the ListCronJob status was updated
	var updated batchopsv1alpha1.ListCronJob
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(listCronJob), &updated)
	require.NoError(t, err)

	require.NotNil(t, updated.Status.LastScheduleTime, "LastScheduleTime should be set")
	// Compare times with 1 second tolerance due to time.Time serialization
	assert.WithinDuration(t, scheduleTime.Time, updated.Status.LastScheduleTime.Time, 1*time.Second)
}

// TestTrackCronJobCycles_SameScheduleTimeNoUpdate verifies that when the CronJob's
// LastScheduleTime hasn't changed (same as ListCronJob's tracked time), no status update
// occurs. This prevents unnecessary API calls and ensures deduplication works correctly.
func TestTrackCronJobCycles_SameScheduleTimeNoUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	scheduleTime := metav1.Now()

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cronjob",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			Schedule:          "*/1 * * * *",
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			StaticList:        []string{"item1"},
			Parallelism:       1,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo"},
			},
		},
		Status: batchopsv1alpha1.ListCronJobStatus{
			LastScheduleTime: &scheduleTime, // Already set
		},
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: &scheduleTime, // Same time
			Active:           []corev1.ObjectReference{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listCronJob).
		WithStatusSubresource(listCronJob).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	err := reconciler.trackCronJobCycles(ctx, listCronJob, cronJob)
	require.NoError(t, err)

	// Verify ResourceVersion didn't change (no update)
	var updated batchopsv1alpha1.ListCronJob
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(listCronJob), &updated)
	require.NoError(t, err)

	assert.Equal(t, "1", updated.ResourceVersion)
}

// TestTrackCronJobCycles_NoScheduleTime verifies that the trackCronJobCycles function
// handles the case where the CronJob has never been scheduled yet (LastScheduleTime is nil).
// It should complete without errors and make no status updates.
func TestTrackCronJobCycles_NoScheduleTime(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			Schedule:    "*/1 * * * *",
			StaticList:  []string{"item1"},
			Parallelism: 1,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo"},
			},
		},
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: nil, // No schedule yet
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listCronJob).
		WithStatusSubresource(listCronJob).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	err := reconciler.trackCronJobCycles(ctx, listCronJob, cronJob)
	require.NoError(t, err)

	// Verify no update occurred
	var updated batchopsv1alpha1.ListCronJob
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(listCronJob), &updated)
	require.NoError(t, err)

	assert.Nil(t, updated.Status.LastScheduleTime)
}

// TestFindListCronJobForCronJob verifies that when a CronJob changes, the watch mapper
// correctly identifies its owning ListCronJob via OwnerReferences and returns a reconcile
// request for it. This enables automatic reconciliation when the underlying CronJob's status changes.
func TestFindListCronJobForCronJob(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-listcronjob",
			Namespace: "default",
			UID:       "test-uid",
		},
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{
					APIVersion: "batchops.io/v1alpha1",
					Kind:       "ListCronJob",
					Name:       "test-listcronjob",
					UID:        "test-uid",
					Controller: func() *bool { b := true; return &b }(),
				},
			},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listCronJob, cronJob).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	requests := reconciler.findListCronJobForCronJob(ctx, cronJob)

	require.Len(t, requests, 1)
	assert.Equal(t, "test-listcronjob", requests[0].Name)
	assert.Equal(t, "default", requests[0].Namespace)
}

// TestFindListCronJobForCronJob_NoOwner verifies that when a CronJob has no OwnerReferences
// (not managed by a ListCronJob), the mapper returns an empty list and doesn't trigger
// any reconciliation. This prevents unnecessary reconciliations for unrelated CronJobs.
func TestFindListCronJobForCronJob_NoOwner(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchv1.AddToScheme(scheme))

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-cronjob",
			Namespace:       "default",
			OwnerReferences: []metav1.OwnerReference{},
		},
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	requests := reconciler.findListCronJobForCronJob(ctx, cronJob)

	assert.Len(t, requests, 0)
}

// TestDetectSkippedCycles_WithJobAlreadyActiveEvent verifies that when a JobAlreadyActive
// event exists for a CronJob, it is detected and logged, and the event UID is tracked in
// status to prevent duplicate logging.
func TestDetectSkippedCycles_WithJobAlreadyActiveEvent(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	recentTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			Schedule:          "*/1 * * * *",
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			StaticList:        []string{"item1"},
			Parallelism:       1,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo"},
			},
		},
		Status: batchopsv1alpha1.ListCronJobStatus{
			LastSkipEventUID: "", // No event processed yet
		},
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: &recentTime,
			Active: []corev1.ObjectReference{
				{Name: "job-1", Namespace: "default"},
			},
		},
	}

	// Create a JobAlreadyActive event
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-event",
			Namespace: "default",
			UID:       "event-uid-123",
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: "batch/v1",
			Kind:       "CronJob",
			Name:       "test-cronjob",
			Namespace:  "default",
		},
		Reason:        "JobAlreadyActive",
		Message:       "Not starting job because 1 active jobs already exist",
		Type:          corev1.EventTypeNormal,
		LastTimestamp: recentTime,
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listCronJob, cronJob, event).
		WithStatusSubresource(listCronJob).
		WithIndex(&corev1.Event{}, "involvedObject.name", func(obj client.Object) []string {
			event := obj.(*corev1.Event)
			return []string{event.InvolvedObject.Name}
		}).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	err := reconciler.detectSkippedCycles(ctx, listCronJob, cronJob)
	require.NoError(t, err)

	// Verify the event UID was tracked in status
	var updated batchopsv1alpha1.ListCronJob
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(listCronJob), &updated)
	require.NoError(t, err)
	assert.Equal(t, "event-uid-123", updated.Status.LastSkipEventUID)
}

// TestDetectSkippedCycles_DuplicateEventNotLogged verifies that when the same event
// UID has already been processed, it is not logged again (deduplication works).
func TestDetectSkippedCycles_DuplicateEventNotLogged(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	recentTime := metav1.NewTime(time.Now().Add(-1 * time.Minute))

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			Schedule:          "*/1 * * * *",
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			StaticList:        []string{"item1"},
			Parallelism:       1,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo"},
			},
		},
		Status: batchopsv1alpha1.ListCronJobStatus{
			LastSkipEventUID: "event-uid-123", // Already processed
		},
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
	}

	// Create the same event that was already processed
	event := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-event",
			Namespace: "default",
			UID:       "event-uid-123", // Same UID as in status
		},
		InvolvedObject: corev1.ObjectReference{
			APIVersion: "batch/v1",
			Kind:       "CronJob",
			Name:       "test-cronjob",
			Namespace:  "default",
		},
		Reason:        "JobAlreadyActive",
		Message:       "Not starting job because 1 active jobs already exist",
		Type:          corev1.EventTypeNormal,
		LastTimestamp: recentTime,
	}

	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listCronJob, cronJob, event).
		WithStatusSubresource(listCronJob).
		WithIndex(&corev1.Event{}, "involvedObject.name", func(obj client.Object) []string {
			event := obj.(*corev1.Event)
			return []string{event.InvolvedObject.Name}
		}).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	err := reconciler.detectSkippedCycles(ctx, listCronJob, cronJob)
	require.NoError(t, err)

	// Verify status wasn't updated (event UID stays the same)
	var updated batchopsv1alpha1.ListCronJob
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(listCronJob), &updated)
	require.NoError(t, err)
	assert.Equal(t, "event-uid-123", updated.Status.LastSkipEventUID)
}

// TestDetectSkippedCycles_NoEventsFound verifies that when no JobAlreadyActive events
// exist for the CronJob, no errors occur and nothing is logged.
func TestDetectSkippedCycles_NoEventsFound(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			Schedule:          "*/1 * * * *",
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			StaticList:        []string{"item1"},
			Parallelism:       1,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo"},
			},
		},
	}

	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
	}

	// No events created
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		WithObjects(listCronJob, cronJob).
		WithStatusSubresource(listCronJob).
		WithIndex(&corev1.Event{}, "involvedObject.name", func(obj client.Object) []string {
			event := obj.(*corev1.Event)
			return []string{event.InvolvedObject.Name}
		}).
		Build()

	reconciler := &ListCronJobReconciler{
		Client: fakeClient,
		Scheme: scheme,
	}

	ctx := context.Background()
	err := reconciler.detectSkippedCycles(ctx, listCronJob, cronJob)
	require.NoError(t, err)

	// Verify no status update occurred
	var updated batchopsv1alpha1.ListCronJob
	err = fakeClient.Get(ctx, client.ObjectKeyFromObject(listCronJob), &updated)
	require.NoError(t, err)
	assert.Empty(t, updated.Status.LastSkipEventUID)
}
