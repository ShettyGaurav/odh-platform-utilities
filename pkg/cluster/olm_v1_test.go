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
