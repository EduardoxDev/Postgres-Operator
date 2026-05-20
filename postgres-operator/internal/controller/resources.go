package controller

import (
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"

	databasesv1alpha1 "github.com/example/postgres-operator/api/v1alpha1"
)

const (
	pgPort     = 5432
	pgDataPath = "/var/lib/postgresql/data/pgdata"
)

// buildStatefulSet constructs the StatefulSet spec for a PostgresDatabase.
//
// Topology:
//   - Pod 0  → primary  (accepts reads + writes)
//   - Pod 1+ → replicas (streaming replication from pod 0, read-only)
//
// An init container on replica pods runs pg_basebackup against the primary
// before the main postgres process starts, and writes standby.signal so
// PostgreSQL enters hot-standby mode automatically.
func buildStatefulSet(db *databasesv1alpha1.PostgresDatabase, secretName string) *appsv1.StatefulSet {
	image := fmt.Sprintf("postgres:%s", db.Spec.Version)
	labels := labelsFor(db)
	headlessSvcName := db.Name + "-headless"
	replicas := db.Spec.Replicas

	// ---------- init container (replica setup) ----------
	// On pod 0 the script exits immediately — it is only active on replicas.
	initScript := fmt.Sprintf(`#!/bin/bash
set -euo pipefail

ORDINAL=${HOSTNAME##*-}
if [ "$ORDINAL" = "0" ]; then
  echo "Primary pod — skipping replica init"
  exit 0
fi

PRIMARY="%s-0.%s.%s.svc.cluster.local"
echo "Replica pod $ORDINAL — waiting for primary $PRIMARY..."
until pg_isready -h "$PRIMARY" -p 5432 -U "$POSTGRES_USER"; do
  sleep 2
done

if [ -f "$PGDATA/PG_VERSION" ]; then
  echo "Data directory already populated — skipping pg_basebackup"
  exit 0
fi

echo "Running pg_basebackup from $PRIMARY"
PGPASSWORD="$POSTGRES_PASSWORD" pg_basebackup \
  -h "$PRIMARY" -p 5432 -U "$POSTGRES_USER" \
  -D "$PGDATA" -Fp -Xs -R -P --checkpoint=fast

echo "Replica init complete"
`, db.Name, headlessSvcName, db.Namespace)

	initContainer := corev1.Container{
		Name:    "replica-init",
		Image:   image,
		Command: []string{"/bin/bash", "-c", initScript},
		Env:     postgresEnvVars(db.Spec.Username, db.Spec.Database, secretName),
		VolumeMounts: []corev1.VolumeMount{
			{Name: "data", MountPath: "/var/lib/postgresql/data"},
		},
	}

	// ---------- main postgres container ----------
	mainContainer := corev1.Container{
		Name:  "postgres",
		Image: image,
		Ports: []corev1.ContainerPort{
			{Name: "postgres", ContainerPort: pgPort, Protocol: corev1.ProtocolTCP},
		},
		Env: append(
			postgresEnvVars(db.Spec.Username, db.Spec.Database, secretName),
			// Allow replicas to connect for replication.
			corev1.EnvVar{Name: "POSTGRES_HOST_AUTH_METHOD", Value: "md5"},
		),
		Resources: db.Spec.Resources,
		VolumeMounts: []corev1.VolumeMount{
			{Name: "data", MountPath: "/var/lib/postgresql/data"},
		},
		LivenessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"pg_isready", "-U", db.Spec.Username, "-d", db.Spec.Database},
				},
			},
			InitialDelaySeconds: 30,
			PeriodSeconds:       10,
			TimeoutSeconds:      5,
			FailureThreshold:    6,
		},
		ReadinessProbe: &corev1.Probe{
			ProbeHandler: corev1.ProbeHandler{
				Exec: &corev1.ExecAction{
					Command: []string{"pg_isready", "-U", db.Spec.Username, "-d", db.Spec.Database},
				},
			},
			InitialDelaySeconds: 5,
			PeriodSeconds:       5,
			TimeoutSeconds:      3,
			FailureThreshold:    3,
		},
	}

	// ---------- PVC template ----------
	storageSize := db.Spec.Storage.Size
	if storageSize.IsZero() {
		storageSize = resource.MustParse("1Gi")
	}
	pvcTemplate := corev1.PersistentVolumeClaim{
		ObjectMeta: metav1.ObjectMeta{Name: "data"},
		Spec: corev1.PersistentVolumeClaimSpec{
			AccessModes: []corev1.PersistentVolumeAccessMode{corev1.ReadWriteOnce},
			Resources: corev1.VolumeResourceRequirements{
				Requests: corev1.ResourceList{
					corev1.ResourceStorage: storageSize,
				},
			},
			StorageClassName: db.Spec.Storage.StorageClassName,
		},
	}

	sts := &appsv1.StatefulSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name,
			Namespace: db.Namespace,
			Labels:    labels,
		},
		Spec: appsv1.StatefulSetSpec{
			ServiceName:         headlessSvcName,
			Replicas:            &replicas,
			PodManagementPolicy: appsv1.OrderedReadyPodManagement,
			Selector: &metav1.LabelSelector{
				MatchLabels: labels,
			},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: labels},
				Spec: corev1.PodSpec{
					InitContainers: []corev1.Container{initContainer},
					Containers:     []corev1.Container{mainContainer},
				},
			},
			VolumeClaimTemplates: []corev1.PersistentVolumeClaim{pvcTemplate},
			UpdateStrategy: appsv1.StatefulSetUpdateStrategy{
				Type: appsv1.RollingUpdateStatefulSetStrategyType,
			},
		},
	}
	return sts
}

// buildHeadlessService builds the headless Service that gives each pod a
// stable DNS name: <name>-<ordinal>.<headless-svc>.<ns>.svc.cluster.local
func buildHeadlessService(db *databasesv1alpha1.PostgresDatabase) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name + "-headless",
			Namespace: db.Namespace,
			Labels:    labelsFor(db),
			Annotations: map[string]string{
				"service.alpha.kubernetes.io/tolerate-unready-endpoints": "true",
			},
		},
		Spec: corev1.ServiceSpec{
			ClusterIP:                "None",
			PublishNotReadyAddresses: true,
			Selector:                 labelsFor(db),
			Ports: []corev1.ServicePort{
				{
					Name:       "postgres",
					Port:       pgPort,
					TargetPort: intstr.FromInt(pgPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// buildService builds the regular ClusterIP Service that routes to the primary.
// In a production operator this would use a label selector that only matches
// the primary pod; here we rely on clients using the headless DNS directly
// for read replicas and this service for the primary.
func buildService(db *databasesv1alpha1.PostgresDatabase) *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      db.Name + "-svc",
			Namespace: db.Namespace,
			Labels:    labelsFor(db),
		},
		Spec: corev1.ServiceSpec{
			Type:     corev1.ServiceTypeClusterIP,
			Selector: labelsFor(db),
			Ports: []corev1.ServicePort{
				{
					Name:       "postgres",
					Port:       pgPort,
					TargetPort: intstr.FromInt(pgPort),
					Protocol:   corev1.ProtocolTCP,
				},
			},
		},
	}
}

// labelsFor returns the standard label set applied to all child resources.
func labelsFor(db *databasesv1alpha1.PostgresDatabase) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       "postgres",
		"app.kubernetes.io/instance":   db.Name,
		"app.kubernetes.io/managed-by": "postgres-operator",
	}
}

// postgresEnvVars returns the standard environment variables consumed by the
// official postgres Docker image.
func postgresEnvVars(username, database, secretName string) []corev1.EnvVar {
	secretRef := func(key string) *corev1.EnvVarSource {
		return &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{Name: secretName},
				Key:                  key,
			},
		}
	}
	return []corev1.EnvVar{
		{Name: "POSTGRES_USER", Value: username},
		{Name: "POSTGRES_DB", Value: database},
		{Name: "POSTGRES_PASSWORD", ValueFrom: secretRef(passwordKeyName)},
		{Name: "PGDATA", Value: pgDataPath},
	}
}
