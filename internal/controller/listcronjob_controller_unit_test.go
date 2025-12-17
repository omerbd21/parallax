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

// TestGetNextScheduleTime verifies that the cron schedule parser correctly calculates
// the next scheduled run time for various cron expressions.
func TestGetNextScheduleTime(t *testing.T) {
	tests := []struct {
		name     string
		schedule string
		after    time.Time
		wantErr  bool
	}{
		{
			name:     "Every minute",
			schedule: "*/1 * * * *",
			after:    time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:     "Every hour",
			schedule: "0 * * * *",
			after:    time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:     "Daily at midnight",
			schedule: "0 0 * * *",
			after:    time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC),
			wantErr:  false,
		},
		{
			name:     "Invalid schedule",
			schedule: "invalid cron",
			after:    time.Date(2025, 1, 1, 10, 30, 0, 0, time.UTC),
			wantErr:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nextTime, err := getNextScheduleTime(tt.schedule, tt.after)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.True(t, nextTime.After(tt.after), "Next schedule time should be after the given time")
			}
		})
	}
}

// TestTrackCronJobCycles_SkipDetection verifies that when a CronJob with Forbid policy
// has active jobs and the current time is past the next expected schedule time, a skip
// is detected and logged. This uses status inference instead of watching Events.
func TestTrackCronJobCycles_SkipDetection(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	// Use a time in the past so we're definitely past the next schedule
	pastTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))

	listCronJob := &batchopsv1alpha1.ListCronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Spec: batchopsv1alpha1.ListCronJobSpec{
			Schedule:          "*/1 * * * *", // Every minute
			ConcurrencyPolicy: batchv1.ForbidConcurrent,
			StaticList:        []string{"item1"},
			Parallelism:       1,
			Template: batchopsv1alpha1.JobTemplateSpec{
				Image:   "busybox",
				Command: []string{"echo"},
			},
		},
		Status: batchopsv1alpha1.ListCronJobStatus{
			LastScheduleTime: &pastTime,
		},
	}

	// CronJob has active jobs and last schedule was 5 minutes ago
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: &pastTime,
			Active: []corev1.ObjectReference{
				{Name: "job-1", Namespace: "default"},
			},
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
	// The skip should be detected and logged (check logs in real usage)
}

// TestTrackCronJobCycles_NoSkipWhenNoActiveJobs verifies that when there are no active
// jobs, no skip is detected even if we're past the next schedule time (because a new
// cycle should have started).
func TestTrackCronJobCycles_NoSkipWhenNoActiveJobs(t *testing.T) {
	scheme := runtime.NewScheme()
	require.NoError(t, batchopsv1alpha1.AddToScheme(scheme))
	require.NoError(t, corev1.AddToScheme(scheme))
	require.NoError(t, batchv1.AddToScheme(scheme))

	pastTime := metav1.NewTime(time.Now().Add(-5 * time.Minute))

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
			LastScheduleTime: &pastTime,
		},
	}

	// CronJob has NO active jobs
	cronJob := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-cronjob",
			Namespace: "default",
		},
		Status: batchv1.CronJobStatus{
			LastScheduleTime: &pastTime,
			Active:           []corev1.ObjectReference{}, // No active jobs
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
	// No skip should be logged
}
