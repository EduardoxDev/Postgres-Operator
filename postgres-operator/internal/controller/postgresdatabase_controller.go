package controller

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/tools/record"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	databasesv1alpha1 "github.com/example/postgres-operator/api/v1alpha1"
)

const (
	finalizerName   = "databases.example.io/finalizer"
	passwordKeyName = "password"
)

// PostgresDatabaseReconciler reconciles PostgresDatabase objects.
//
// +kubebuilder:rbac:groups=databases.example.io,resources=postgresdatabases,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=databases.example.io,resources=postgresdatabases/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=databases.example.io,resources=postgresdatabases/finalizers,verbs=update
// +kubebuilder:rbac:groups=apps,resources=statefulsets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=secrets,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=core,resources=events,verbs=create;patch
type PostgresDatabaseReconciler struct {
	client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *PostgresDatabaseReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	db := &databasesv1alpha1.PostgresDatabase{}
	if err := r.Get(ctx, req.NamespacedName, db); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	// Handle deletion via finalizer.
	if !db.DeletionTimestamp.IsZero() {
		return r.handleDeletion(ctx, db)
	}

	// Ensure our finalizer is registered.
	if !controllerutil.ContainsFinalizer(db, finalizerName) {
		controllerutil.AddFinalizer(db, finalizerName)
		if err := r.Update(ctx, db); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}

	// Reconcile child resources in dependency order.
	secretName, err := r.reconcileSecret(ctx, db)
	if err != nil {
		logger.Error(err, "failed to reconcile Secret")
		r.setCondition(db, databasesv1alpha1.ConditionReady, metav1.ConditionFalse, "SecretError", err.Error())
		_ = r.Status().Update(ctx, db)
		return ctrl.Result{}, err
	}

	if err := r.reconcileHeadlessService(ctx, db); err != nil {
		logger.Error(err, "failed to reconcile headless Service")
		return ctrl.Result{}, err
	}

	if err := r.reconcileService(ctx, db); err != nil {
		logger.Error(err, "failed to reconcile Service")
		return ctrl.Result{}, err
	}

	if err := r.reconcileStatefulSet(ctx, db, secretName); err != nil {
		logger.Error(err, "failed to reconcile StatefulSet")
		r.setCondition(db, databasesv1alpha1.ConditionReady, metav1.ConditionFalse, "StatefulSetError", err.Error())
		_ = r.Status().Update(ctx, db)
		return ctrl.Result{}, err
	}

	return r.updateStatus(ctx, db, secretName)
}

// handleDeletion runs cleanup before the object is removed from etcd.
func (r *PostgresDatabaseReconciler) handleDeletion(ctx context.Context, db *databasesv1alpha1.PostgresDatabase) (ctrl.Result, error) {
	if !controllerutil.ContainsFinalizer(db, finalizerName) {
		return ctrl.Result{}, nil
	}
	// All owned child resources are garbage-collected automatically via
	// OwnerReferences, so no manual deletion is needed here.
	controllerutil.RemoveFinalizer(db, finalizerName)
	return ctrl.Result{}, r.Update(ctx, db)
}

// reconcileSecret ensures the credentials Secret exists.
// Returns the name of the secret that holds the password.
func (r *PostgresDatabaseReconciler) reconcileSecret(ctx context.Context, db *databasesv1alpha1.PostgresDatabase) (string, error) {
	// If the user pointed us at an existing secret, trust it.
	if db.Spec.PasswordSecretRef != nil {
		return db.Spec.PasswordSecretRef.Name, nil
	}

	secretName := db.Name + "-credentials"
	secret := &corev1.Secret{}
	err := r.Get(ctx, types.NamespacedName{Name: secretName, Namespace: db.Namespace}, secret)
	if err == nil {
		// Secret already exists — nothing to do.
		return secretName, nil
	}
	if !apierrors.IsNotFound(err) {
		return "", err
	}

	// Generate a random 24-byte password.
	raw := make([]byte, 24)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generating password: %w", err)
	}
	password := base64.URLEncoding.EncodeToString(raw)

	desired := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      secretName,
			Namespace: db.Namespace,
		},
		StringData: map[string]string{
			passwordKeyName: password,
			"username":      db.Spec.Username,
			"database":      db.Spec.Database,
			// Convenience DSN for application use.
			"dsn": fmt.Sprintf("postgres://%s:%s@%s-svc.%s.svc.cluster.local:5432/%s?sslmode=disable",
				db.Spec.Username, password, db.Name, db.Namespace, db.Spec.Database),
		},
	}
	if err := controllerutil.SetControllerReference(db, desired, r.Scheme); err != nil {
		return "", err
	}

	if err := r.Create(ctx, desired); err != nil {
		return "", fmt.Errorf("creating credentials secret: %w", err)
	}
	r.Recorder.Eventf(db, corev1.EventTypeNormal, "SecretCreated", "Created credentials secret %s", secretName)
	return secretName, nil
}

// reconcileHeadlessService creates/updates the headless Service used for
// stable pod DNS entries (e.g. <name>-0.<headless-svc>.<ns>.svc.cluster.local).
func (r *PostgresDatabaseReconciler) reconcileHeadlessService(ctx context.Context, db *databasesv1alpha1.PostgresDatabase) error {
	desired := buildHeadlessService(db)
	if err := controllerutil.SetControllerReference(db, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating headless service: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}

	existing.Spec.Selector = desired.Spec.Selector
	return r.Update(ctx, existing)
}

// reconcileService creates/updates the ClusterIP Service for the primary.
func (r *PostgresDatabaseReconciler) reconcileService(ctx context.Context, db *databasesv1alpha1.PostgresDatabase) error {
	desired := buildService(db)
	if err := controllerutil.SetControllerReference(db, desired, r.Scheme); err != nil {
		return err
	}

	existing := &corev1.Service{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating service: %w", err)
		}
		return nil
	}
	if err != nil {
		return err
	}

	existing.Spec.Selector = desired.Spec.Selector
	existing.Spec.Ports = desired.Spec.Ports
	return r.Update(ctx, existing)
}

// reconcileStatefulSet creates/updates the StatefulSet that runs PostgreSQL.
func (r *PostgresDatabaseReconciler) reconcileStatefulSet(ctx context.Context, db *databasesv1alpha1.PostgresDatabase, secretName string) error {
	desired := buildStatefulSet(db, secretName)
	if err := controllerutil.SetControllerReference(db, desired, r.Scheme); err != nil {
		return err
	}

	existing := &appsv1.StatefulSet{}
	err := r.Get(ctx, types.NamespacedName{Name: desired.Name, Namespace: desired.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		if err := r.Create(ctx, desired); err != nil {
			return fmt.Errorf("creating statefulset: %w", err)
		}
		r.Recorder.Eventf(db, corev1.EventTypeNormal, "StatefulSetCreated",
			"Created StatefulSet %s with %d replica(s)", desired.Name, db.Spec.Replicas)
		return nil
	}
	if err != nil {
		return err
	}

	// Only update mutable fields to avoid fighting the StatefulSet controller.
	existing.Spec.Replicas = desired.Spec.Replicas
	existing.Spec.Template.Spec.Containers = desired.Spec.Template.Spec.Containers
	existing.Spec.Template.Spec.InitContainers = desired.Spec.Template.Spec.InitContainers
	return r.Update(ctx, existing)
}

// updateStatus syncs the PostgresDatabase status from the live StatefulSet.
func (r *PostgresDatabaseReconciler) updateStatus(ctx context.Context, db *databasesv1alpha1.PostgresDatabase, secretName string) (ctrl.Result, error) {
	sts := &appsv1.StatefulSet{}
	if err := r.Get(ctx, types.NamespacedName{Name: db.Name, Namespace: db.Namespace}, sts); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	db.Status.ReadyReplicas = sts.Status.ReadyReplicas
	db.Status.SecretName = secretName
	db.Status.ServiceName = db.Name + "-svc"

	switch {
	case sts.Status.ReadyReplicas == db.Spec.Replicas:
		db.Status.Phase = databasesv1alpha1.PhaseRunning
		r.setCondition(db, databasesv1alpha1.ConditionReady, metav1.ConditionTrue, "AllReplicasReady",
			fmt.Sprintf("%d/%d replicas ready", sts.Status.ReadyReplicas, db.Spec.Replicas))
		r.setCondition(db, databasesv1alpha1.ConditionProgressing, metav1.ConditionFalse, "ReconcileComplete", "")
	case sts.Status.ReadyReplicas > 0:
		db.Status.Phase = databasesv1alpha1.PhaseRunning
		r.setCondition(db, databasesv1alpha1.ConditionReady, metav1.ConditionFalse, "PartiallyReady",
			fmt.Sprintf("%d/%d replicas ready", sts.Status.ReadyReplicas, db.Spec.Replicas))
		r.setCondition(db, databasesv1alpha1.ConditionProgressing, metav1.ConditionTrue, "Scaling", "")
	default:
		db.Status.Phase = databasesv1alpha1.PhasePending
		r.setCondition(db, databasesv1alpha1.ConditionReady, metav1.ConditionFalse, "NotReady", "No replicas ready yet")
		r.setCondition(db, databasesv1alpha1.ConditionProgressing, metav1.ConditionTrue, "Starting", "")
	}

	if err := r.Status().Update(ctx, db); err != nil {
		return ctrl.Result{}, err
	}

	// Re-queue until all replicas are ready.
	if db.Status.ReadyReplicas < db.Spec.Replicas {
		return ctrl.Result{RequeueAfter: 15_000_000_000 /* 15s */}, nil
	}
	return ctrl.Result{}, nil
}

// setCondition upserts a metav1.Condition on the status.
func (r *PostgresDatabaseReconciler) setCondition(db *databasesv1alpha1.PostgresDatabase, condType string, status metav1.ConditionStatus, reason, message string) {
	now := metav1.Now()
	for i, c := range db.Status.Conditions {
		if c.Type == condType {
			if c.Status == status && c.Reason == reason {
				return // no change
			}
			db.Status.Conditions[i] = metav1.Condition{
				Type:               condType,
				Status:             status,
				Reason:             reason,
				Message:            message,
				LastTransitionTime: now,
			}
			return
		}
	}
	db.Status.Conditions = append(db.Status.Conditions, metav1.Condition{
		Type:               condType,
		Status:             status,
		Reason:             reason,
		Message:            message,
		LastTransitionTime: now,
	})
}

// SetupWithManager registers the controller with the manager and sets up
// watches so that changes to owned StatefulSets/Services trigger reconciles.
func (r *PostgresDatabaseReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&databasesv1alpha1.PostgresDatabase{}).
		Owns(&appsv1.StatefulSet{}).
		Owns(&corev1.Service{}).
		Owns(&corev1.Secret{}).
		Complete(r)
}
