/*
 Copyright 2021 Crunchy Data Solutions, Inc.
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

package pgbouncer

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"gotest.tools/v3/assert"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"

	"github.com/crunchydata/postgres-operator/internal/pki"
	"github.com/crunchydata/postgres-operator/internal/postgres"
	"github.com/crunchydata/postgres-operator/pkg/apis/postgres-operator.crunchydata.com/v1beta1"
)

func TestConfigMap(t *testing.T) {
	t.Parallel()

	cluster := new(v1beta1.PostgresCluster)
	config := new(corev1.ConfigMap)

	t.Run("Disabled", func(t *testing.T) {
		// Nothing happens when PgBouncer is disabled.
		constant := config.DeepCopy()
		ConfigMap(cluster, config)
		assert.DeepEqual(t, constant, config)
	})

	cluster.Spec.Proxy = new(v1beta1.PostgresProxySpec)
	cluster.Spec.Proxy.PGBouncer = new(v1beta1.PGBouncerPodSpec)
	cluster.Default()

	ConfigMap(cluster, config)

	// The output of clusterINI should go into config.
	data := clusterINI(cluster)
	assert.DeepEqual(t, config.Data["pgbouncer.ini"], data)

	// No change when called again.
	before := config.DeepCopy()
	ConfigMap(cluster, config)
	assert.DeepEqual(t, before, config)
}

func TestSecret(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	cluster := new(v1beta1.PostgresCluster)
	service := new(corev1.Service)
	existing := new(corev1.Secret)
	intent := new(corev1.Secret)

	root := pki.NewRootCertificateAuthority()
	assert.NilError(t, root.Generate())

	t.Run("Disabled", func(t *testing.T) {
		// Nothing happens when PgBouncer is disabled.
		constant := intent.DeepCopy()
		assert.NilError(t, Secret(ctx, cluster, root, existing, service, intent))
		assert.DeepEqual(t, constant, intent)
	})

	cluster.Spec.Proxy = new(v1beta1.PostgresProxySpec)
	cluster.Spec.Proxy.PGBouncer = new(v1beta1.PGBouncerPodSpec)
	cluster.Default()

	constant := existing.DeepCopy()
	assert.NilError(t, Secret(ctx, cluster, root, existing, service, intent))
	assert.DeepEqual(t, constant, existing)

	// A password should be generated.
	assert.Assert(t, len(intent.Data["pgbouncer-verifier"]) != 0)

	// The output of authFileContents should go into intent.
	assert.Assert(t, len(intent.Data["pgbouncer-users.txt"]) != 0)

	// Assuming the intent is written, no change when called again.
	existing.Data = intent.Data
	before := intent.DeepCopy()
	assert.NilError(t, Secret(ctx, cluster, root, existing, service, intent))
	assert.DeepEqual(t, before, intent)
}

func TestPod(t *testing.T) {
	t.Parallel()

	cluster := new(v1beta1.PostgresCluster)
	configMap := new(corev1.ConfigMap)
	primaryCertificate := new(corev1.SecretProjection)
	secret := new(corev1.Secret)
	pod := new(corev1.PodSpec)

	call := func() { Pod(cluster, configMap, primaryCertificate, secret, pod) }

	t.Run("Disabled", func(t *testing.T) {
		before := pod.DeepCopy()
		call()

		// No change when PgBouncer is not requested in the spec.
		assert.DeepEqual(t, before, pod)
	})

	t.Run("Defaults", func(t *testing.T) {
		cluster.Spec.Proxy = new(v1beta1.PostgresProxySpec)
		cluster.Spec.Proxy.PGBouncer = new(v1beta1.PGBouncerPodSpec)
		cluster.Default()

		call()

		assert.Assert(t, marshalEquals(pod, strings.Trim(`
containers:
- command:
  - pgbouncer
  - /etc/pgbouncer/~postgres-operator.ini
  name: pgbouncer
  ports:
  - containerPort: 5432
    name: pgbouncer
    protocol: TCP
  resources: {}
  securityContext:
    allowPrivilegeEscalation: false
    privileged: false
    readOnlyRootFilesystem: true
    runAsNonRoot: true
  volumeMounts:
  - mountPath: /etc/pgbouncer
    name: pgbouncer-config
    readOnly: true
  - mountPath: /etc/pgbouncer/~postgres-operator-backend
    name: pgbouncer-backend-tls
    readOnly: true
  - mountPath: /etc/pgbouncer/~postgres-operator-frontend
    name: pgbouncer-frontend-tls
    readOnly: true
- command:
  - bash
  - -ceu
  - --
  - |-
    monitor() {
    declare -r directory="${directory:-$1}"
    while sleep 5s; do
      mounted=$(stat --format=%y "${directory}")
      if [ "${mounted}" != "${loaded-}" ] && pkill --signal HUP --exact pgbouncer
      then
        loaded="${mounted}"
        echo Loaded configuration dated "${loaded}"
      fi
    done
    }; export directory="$1"; export -f monitor; exec -a "$0" bash -ceu monitor
  - pgbouncer-config
  - /etc/pgbouncer
  name: pgbouncer-config
  resources: {}
  securityContext:
    allowPrivilegeEscalation: false
    privileged: false
    readOnlyRootFilesystem: true
    runAsNonRoot: true
  volumeMounts:
  - mountPath: /etc/pgbouncer
    name: pgbouncer-config
    readOnly: true
volumes:
- name: pgbouncer-backend-tls
  projected:
    sources:
    - secret: {}
- name: pgbouncer-config
  projected:
    sources:
    - configMap:
        items:
        - key: pgbouncer-empty
          path: pgbouncer.ini
    - configMap:
        items:
        - key: pgbouncer.ini
          path: ~postgres-operator.ini
    - secret:
        items:
        - key: pgbouncer-users.txt
          path: ~postgres-operator/users.txt
- name: pgbouncer-frontend-tls
  projected:
    sources:
    - secret:
        items:
        - key: pgbouncer-frontend.ca-roots
          path: ca.crt
        - key: pgbouncer-frontend.key
          path: tls.key
        - key: pgbouncer-frontend.crt
          path: tls.crt
		`, "\t\n")+"\n"))

		// No change when called again.
		before := pod.DeepCopy()
		call()
		assert.DeepEqual(t, before, pod)
	})

	t.Run("Customizations", func(t *testing.T) {
		cluster.Spec.Proxy.PGBouncer.Image = "image-town"
		cluster.Spec.Proxy.PGBouncer.Resources.Requests = corev1.ResourceList{
			corev1.ResourceCPU: resource.MustParse("100m"),
		}
		cluster.Spec.Proxy.PGBouncer.CustomTLSSecret = &corev1.SecretProjection{
			LocalObjectReference: corev1.LocalObjectReference{Name: "tls-name"},
			Items: []corev1.KeyToPath{
				{Key: "k1", Path: "p1"},
			},
		}

		call()

		assert.Assert(t, marshalEquals(pod,
			strings.Trim(`
containers:
- command:
  - pgbouncer
  - /etc/pgbouncer/~postgres-operator.ini
  image: image-town
  name: pgbouncer
  ports:
  - containerPort: 5432
    name: pgbouncer
    protocol: TCP
  resources:
    requests:
      cpu: 100m
  securityContext:
    allowPrivilegeEscalation: false
    privileged: false
    readOnlyRootFilesystem: true
    runAsNonRoot: true
  volumeMounts:
  - mountPath: /etc/pgbouncer
    name: pgbouncer-config
    readOnly: true
  - mountPath: /etc/pgbouncer/~postgres-operator-backend
    name: pgbouncer-backend-tls
    readOnly: true
  - mountPath: /etc/pgbouncer/~postgres-operator-frontend
    name: pgbouncer-frontend-tls
    readOnly: true
- command:
  - bash
  - -ceu
  - --
  - |-
    monitor() {
    declare -r directory="${directory:-$1}"
    while sleep 5s; do
      mounted=$(stat --format=%y "${directory}")
      if [ "${mounted}" != "${loaded-}" ] && pkill --signal HUP --exact pgbouncer
      then
        loaded="${mounted}"
        echo Loaded configuration dated "${loaded}"
      fi
    done
    }; export directory="$1"; export -f monitor; exec -a "$0" bash -ceu monitor
  - pgbouncer-config
  - /etc/pgbouncer
  image: image-town
  name: pgbouncer-config
  resources:
    limits:
      cpu: 5m
      memory: 16Mi
  securityContext:
    allowPrivilegeEscalation: false
    privileged: false
    readOnlyRootFilesystem: true
    runAsNonRoot: true
  volumeMounts:
  - mountPath: /etc/pgbouncer
    name: pgbouncer-config
    readOnly: true
volumes:
- name: pgbouncer-backend-tls
  projected:
    sources:
    - secret: {}
- name: pgbouncer-config
  projected:
    sources:
    - configMap:
        items:
        - key: pgbouncer-empty
          path: pgbouncer.ini
    - configMap:
        items:
        - key: pgbouncer.ini
          path: ~postgres-operator.ini
    - secret:
        items:
        - key: pgbouncer-users.txt
          path: ~postgres-operator/users.txt
- name: pgbouncer-frontend-tls
  projected:
    sources:
    - secret:
        items:
        - key: k1
          path: p1
        name: tls-name
			`, "\t\n")+"\n"))
	})
}

func TestPostgreSQL(t *testing.T) {
	t.Parallel()

	cluster := new(v1beta1.PostgresCluster)
	hbas := new(postgres.HBAs)

	t.Run("Disabled", func(t *testing.T) {
		PostgreSQL(cluster, hbas)

		// No change when PgBouncer is not requested in the spec.
		assert.DeepEqual(t, hbas, new(postgres.HBAs))
	})

	t.Run("Enabled", func(t *testing.T) {
		cluster.Spec.Proxy = new(v1beta1.PostgresProxySpec)
		cluster.Spec.Proxy.PGBouncer = new(v1beta1.PGBouncerPodSpec)
		cluster.Default()

		PostgreSQL(cluster, hbas)

		assert.DeepEqual(t, hbas,
			&postgres.HBAs{
				Mandatory: []postgres.HostBasedAuthentication{postgresqlHBA()},
			},
			// postgres.HostBasedAuthentication has unexported fields. Call String() to compare.
			cmp.Transformer("", postgres.HostBasedAuthentication.String))
	})
}
