package cluster_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/opendatahub-io/odh-platform-utilities/pkg/cluster"
)

var errClusterExtensionListFailure = errors.New("list failure")

func TestClusterExtensionInstallsPackage(t *testing.T) { //nolint:funlen // Table-driven coverage.
	t.Parallel()

	clusterExtensionGVK := schema.GroupVersionKind{
		Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterExtension",
	}

	tests := []struct { //nolint:govet // fieldalignment: test table struct
		name             string
		packageName      string
		installNamespace string
		wantErr          string
		wantNoMatch      bool
		listErr          error
		objects          []client.Object
		want             bool
	}{
		{
			name:        "package matches with Catalog source",
			packageName: "rhods-operator",
			objects: []client.Object{
				newClusterExtension("rhoai-ext", "rhods-operator", "redhat-ods-operator"),
			},
			want: true,
		},
		{
			name:        "different package does not match",
			packageName: "rhods-operator",
			objects: []client.Object{
				newClusterExtension("odh-ext", "opendatahub-operator", "opendatahub-operator-system"),
			},
		},
		{
			name:        "non-Catalog sourceType does not match",
			packageName: "rhods-operator",
			objects: []client.Object{
				newClusterExtensionWithSourceType("rhoai-ext", "rhods-operator", "Bundle"),
			},
		},
		{
			name:             "namespace filter matches",
			packageName:      "rhods-operator",
			installNamespace: "redhat-ods-operator",
			objects: []client.Object{
				newClusterExtension("rhoai-ext", "rhods-operator", "redhat-ods-operator"),
			},
			want: true,
		},
		{
			name:             "namespace filter rejects different namespace",
			packageName:      "rhods-operator",
			installNamespace: "redhat-ods-operator",
			objects: []client.Object{
				newClusterExtension("rhoai-ext", "rhods-operator", "other-namespace"),
			},
		},
		{
			name:        "missing package field",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionWithoutPackageName("rhoai-ext"),
			},
		},
		{
			name:        "malformed package field",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionWithMalformedPackageName("rhoai-ext"),
			},
			wantErr: "read ClusterExtension spec.source.catalog.packageName",
		},
		{
			name:        "ClusterExtension API unavailable",
			packageName: "rhods-operator",
			listErr: &meta.NoKindMatchError{
				GroupKind: clusterExtensionGVK.GroupKind(),
			},
			wantNoMatch: true,
		},
		{
			name:        "list API failure propagated",
			packageName: "rhods-operator",
			listErr:     errClusterExtensionListFailure,
			wantErr:     "list failure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			baseCli := fake.NewClientBuilder().
				WithScheme(runtime.NewScheme()).
				WithObjects(tc.objects...).
				Build()
			cli := &gvkListErrorClient{
				Reader: baseCli,
				err:    tc.listErr,
				errGVK: clusterExtensionGVK,
			}

			found, err := cluster.ClusterExtensionInstallsPackage(
				t.Context(), cli, tc.packageName, tc.installNamespace,
			)
			switch {
			case tc.wantErr != "":
				require.ErrorContains(t, err, tc.wantErr)
			case tc.wantNoMatch:
				require.True(t, meta.IsNoMatchError(err))
			default:
				require.NoError(t, err)
			}

			assert.Equal(t, tc.want, found)
		})
	}
}

func TestOperatorInstalledViaClusterExtension(t *testing.T) { //nolint:funlen // Table-driven coverage.
	t.Parallel()

	clusterExtensionGVK := schema.GroupVersionKind{
		Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterExtension",
	}

	tests := []struct { //nolint:govet // fieldalignment: test table struct
		name        string
		packageName string
		wantErr     string
		wantNoMatch bool
		listErr     error
		objects     []client.Object
		wantVersion string
		wantInfo    bool
	}{
		{
			name:        "installed extension returns version",
			packageName: "rhods-operator",
			objects: []client.Object{
				newInstalledClusterExtension("rhoai-ext", "rhods-operator", "1.2.3"),
			},
			wantInfo:    true,
			wantVersion: "v1.2.3",
		},
		{
			name:        "installed extension with absent bundle version",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionInstalledWithoutVersion("rhoai-ext", "rhods-operator"),
			},
			wantInfo: true,
		},
		{
			name:        "package requested but not installed",
			packageName: "rhods-operator",
			objects: []client.Object{
				newClusterExtension("rhoai-ext", "rhods-operator", "redhat-ods-operator"),
			},
		},
		{
			name:        "Installed condition false",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionWithInstalledCondition("rhoai-ext", "rhods-operator", "False", "Failed", "1.0.0"),
			},
		},
		{
			name:        "Installed true with unexpected reason",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionWithInstalledCondition("rhoai-ext", "rhods-operator", "True", "Progressing", "1.0.0"),
			},
		},
		{
			name:        "different package does not match",
			packageName: "rhods-operator",
			objects: []client.Object{
				newInstalledClusterExtension("odh-ext", "opendatahub-operator", "2.0.0"),
			},
		},
		{
			name:        "non-Catalog sourceType does not match",
			packageName: "rhods-operator",
			objects: []client.Object{
				newInstalledClusterExtensionWithSourceType("rhoai-ext", "rhods-operator", "Bundle", "1.2.3"),
			},
		},
		{
			name:        "malformed package field",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionWithMalformedPackageName("rhoai-ext"),
			},
			wantErr: "read ClusterExtension spec.source.catalog.packageName",
		},
		{
			name:        "malformed status prevents conversion",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionWithMalformedStatus("rhoai-ext", "rhods-operator"),
			},
			wantErr: "convert ClusterExtension status",
		},
		{
			name:        "malformed bundle version field",
			packageName: "rhods-operator",
			objects: []client.Object{
				clusterExtensionWithMalformedBundleVersion("rhoai-ext", "rhods-operator"),
			},
			wantErr: "read ClusterExtension status.install.bundle.version",
		},
		{
			name:        "ClusterExtension API unavailable",
			packageName: "rhods-operator",
			listErr: &meta.NoKindMatchError{
				GroupKind: clusterExtensionGVK.GroupKind(),
			},
			wantNoMatch: true,
		},
		{
			name:        "list API failure propagated",
			packageName: "rhods-operator",
			listErr:     errClusterExtensionListFailure,
			wantErr:     "list failure",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			baseCli := fake.NewClientBuilder().
				WithScheme(runtime.NewScheme()).
				WithObjects(tc.objects...).
				Build()
			cli := &gvkListErrorClient{
				Reader: baseCli,
				err:    tc.listErr,
				errGVK: clusterExtensionGVK,
			}

			info, err := cluster.OperatorInstalledViaClusterExtension(t.Context(), cli, tc.packageName)
			switch {
			case tc.wantErr != "":
				require.ErrorContains(t, err, tc.wantErr)
				assert.Nil(t, info)
			case tc.wantNoMatch:
				require.True(t, meta.IsNoMatchError(err))
				assert.Nil(t, info)
			default:
				require.NoError(t, err)
			}

			if !tc.wantInfo {
				assert.Nil(t, info)

				return
			}

			require.NotNil(t, info)
			assert.Equal(t, tc.wantVersion, info.Version)
		})
	}
}

func newClusterExtensionWithSourceType(name, packageName, sourceType string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "olm.operatorframework.io/v1",
			"kind":       "ClusterExtension",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"source": map[string]any{
					"sourceType": sourceType,
					"catalog":    map[string]any{"packageName": packageName},
				},
			},
		},
	}
}

func clusterExtensionWithoutPackageName(name string) *unstructured.Unstructured {
	ext := newClusterExtension(name, "rhods-operator", "redhat-ods-operator")
	unstructured.RemoveNestedField(ext.Object, "spec", "source", "catalog", "packageName")

	return ext
}

func clusterExtensionWithMalformedPackageName(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "olm.operatorframework.io/v1",
			"kind":       "ClusterExtension",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"source": map[string]any{
					"sourceType": "Catalog",
					"catalog":    map[string]any{"packageName": int64(42)},
				},
			},
		},
	}
}

func newInstalledClusterExtension(name, packageName, version string) *unstructured.Unstructured {
	return newInstalledClusterExtensionWithSourceType(name, packageName, "Catalog", version)
}

func newInstalledClusterExtensionWithSourceType(
	name, packageName, sourceType, version string,
) *unstructured.Unstructured {
	ext := newClusterExtensionWithSourceType(name, packageName, sourceType)
	ext.Object["status"] = map[string]any{
		"conditions": []any{
			map[string]any{
				"type":   "Installed",
				"status": "True",
				"reason": "Succeeded",
			},
		},
		"install": map[string]any{
			"bundle": map[string]any{
				"version": version,
			},
		},
	}

	return ext
}

func clusterExtensionInstalledWithoutVersion(name, packageName string) *unstructured.Unstructured {
	ext := newClusterExtension(name, packageName, "redhat-ods-operator")
	ext.Object["status"] = map[string]any{
		"conditions": []any{
			map[string]any{
				"type":   "Installed",
				"status": "True",
				"reason": "Succeeded",
			},
		},
	}

	return ext
}

func clusterExtensionWithInstalledCondition(
	name, packageName, status, reason, version string,
) *unstructured.Unstructured {
	ext := newClusterExtension(name, packageName, "redhat-ods-operator")
	ext.Object["status"] = map[string]any{
		"conditions": []any{
			map[string]any{
				"type":   "Installed",
				"status": status,
				"reason": reason,
			},
		},
		"install": map[string]any{
			"bundle": map[string]any{
				"version": version,
			},
		},
	}

	return ext
}

func clusterExtensionWithMalformedStatus(name, packageName string) *unstructured.Unstructured {
	ext := newClusterExtension(name, packageName, "redhat-ods-operator")
	ext.Object["status"] = map[string]any{
		"conditions": int64(42),
	}

	return ext
}

func clusterExtensionWithMalformedBundleVersion(name, packageName string) *unstructured.Unstructured {
	ext := newInstalledClusterExtension(name, packageName, "1.2.3")

	_ = unstructured.SetNestedField(ext.Object, int64(42), "status", "install", "bundle", "version")

	return ext
}
