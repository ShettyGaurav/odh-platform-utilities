package cluster_test

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	"github.com/opendatahub-io/odh-platform-utilities/pkg/cluster"
)

var errPlatformAPI = errors.New("platform api error")

func testClusterCatalogGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterCatalog",
	}
}

func testClusterExtensionGVK() schema.GroupVersionKind {
	return schema.GroupVersionKind{
		Group: "olm.operatorframework.io", Version: "v1", Kind: "ClusterExtension",
	}
}

type erroringPlatformClient struct {
	client.Reader

	getErr  error
	listErr error
}

func (c *erroringPlatformClient) Get(
	ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption,
) error {
	if c.getErr != nil {
		return c.getErr
	}

	return c.Reader.Get(ctx, key, obj, opts...)
}

func (c *erroringPlatformClient) List(
	ctx context.Context, list client.ObjectList, opts ...client.ListOption,
) error {
	if c.listErr != nil {
		return c.listErr
	}

	return c.Reader.List(ctx, list, opts...)
}

type gvkGetErrorClient struct {
	client.Reader

	err    error
	errGVK schema.GroupVersionKind
}

func (c *gvkGetErrorClient) Get(
	ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption,
) error {
	if c.err != nil && obj.GetObjectKind().GroupVersionKind() == c.errGVK {
		return c.err
	}

	return c.Reader.Get(ctx, key, obj, opts...)
}

type gvkListErrorClient struct {
	client.Reader

	err    error
	errGVK schema.GroupVersionKind
}

func (c *gvkListErrorClient) List(
	ctx context.Context, list client.ObjectList, opts ...client.ListOption,
) error {
	if c.err != nil && list.GetObjectKind().GroupVersionKind() == c.errGVK {
		return c.err
	}

	return c.Reader.List(ctx, list, opts...)
}

func TestDetectPlatform_ExplicitType(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		platformType string
		want         cluster.Platform
	}{
		{"OpenDataHub", "OpenDataHub", cluster.OpenDataHub},
		{"ManagedRHOAI", "ManagedRHOAI", cluster.ManagedRhoai},
		{"SelfManagedRHOAI", "SelfManagedRHOAI", cluster.SelfManagedRhoai},
		{"XKS", "XKS", cluster.XKS},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build()

			result, err := cluster.DetectPlatform(t.Context(), cli, tc.platformType, "")
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tc.want {
				t.Errorf("DetectPlatform(%q) = %q, want %q", tc.platformType, result, tc.want)
			}
		})
	}
}

func TestDetectPlatform_AutoDetect(t *testing.T) { //nolint:funlen // Table-driven test with many cases.
	t.Parallel()

	tests := []struct {
		name      string
		want      cluster.Platform
		namespace string
		objects   []client.Object
	}{
		{
			name:      "ManagedRhoai detected via CatalogSource",
			namespace: "redhat-ods-operator",
			objects: []client.Object{
				newCatalogSource("addon-managed-odh-catalog", "redhat-ods-operator"),
			},
			want: cluster.ManagedRhoai,
		},
		{
			name:      "ManagedRhoai detected via ClusterCatalog",
			namespace: "redhat-ods-operator",
			objects: []client.Object{
				newClusterCatalog("addon-managed-odh-catalog"),
			},
			want: cluster.ManagedRhoai,
		},
		{
			name: "SelfManagedRhoai detected via OperatorCondition",
			objects: []client.Object{
				newOperatorCondition("rhods-operator.v1.2.3"),
			},
			want: cluster.SelfManagedRhoai,
		},
		{
			name: "SelfManagedRhoai detected via ClusterExtension",
			objects: []client.Object{
				newClusterExtension("rhoai-ext", "rhods-operator", "redhat-ods-operator"),
			},
			want: cluster.SelfManagedRhoai,
		},
		{
			name: "OpenDataHub when only ODH ClusterExtension exists",
			objects: []client.Object{
				newClusterExtension("odh-ext", "opendatahub-operator", "opendatahub-operator-system"),
			},
			want: cluster.OpenDataHub,
		},
		{
			name:    "Fallback to OpenDataHub",
			objects: nil,
			want:    cluster.OpenDataHub,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(tc.objects...).Build()

			result, err := cluster.DetectPlatform(t.Context(), cli, "", tc.namespace)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if result != tc.want {
				t.Errorf("DetectPlatform() = %q, want %q", result, tc.want)
			}
		})
	}
}

func TestDetectPlatform_DefaultNamespace(t *testing.T) {
	t.Parallel()

	cs := newCatalogSource("addon-managed-odh-catalog", "redhat-ods-operator")
	cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(cs).Build()

	result, err := cluster.DetectPlatform(t.Context(), cli, "", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != cluster.ManagedRhoai {
		t.Errorf("DetectPlatform() = %q, want %q (default ns fallback)", result, cluster.ManagedRhoai)
	}
}

func TestDetectPlatform_OLMv0Precedence(t *testing.T) {
	t.Parallel()

	objects := []client.Object{
		newCatalogSource("addon-managed-odh-catalog", "redhat-ods-operator"),
		newClusterCatalog("addon-managed-odh-catalog"),
	}
	cli := fake.NewClientBuilder().WithScheme(runtime.NewScheme()).WithObjects(objects...).Build()

	result, err := cluster.DetectPlatform(t.Context(), cli, "", "redhat-ods-operator")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != cluster.ManagedRhoai {
		t.Errorf("DetectPlatform() = %q, want %q", result, cluster.ManagedRhoai)
	}
}

func TestDetectPlatform_APIErrors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		client  client.Reader
		name    string
		wantErr bool
	}{
		{
			name: "CatalogSource Get error propagated",
			client: &erroringPlatformClient{
				Reader: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
				getErr: errPlatformAPI,
			},
			wantErr: true,
		},
		{
			name: "OperatorCondition List error propagated",
			client: &erroringPlatformClient{
				Reader:  fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
				listErr: errPlatformAPI,
			},
			wantErr: true,
		},
		{
			name: "ClusterCatalog Get error propagated after CatalogSource miss",
			client: &gvkGetErrorClient{
				Reader: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
				err:    errPlatformAPI,
				errGVK: testClusterCatalogGVK(),
			},
			wantErr: true,
		},
		{
			name: "ClusterExtension List error propagated after OperatorCondition miss",
			client: &gvkListErrorClient{
				Reader: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
				err:    errPlatformAPI,
				errGVK: testClusterExtensionGVK(),
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			_, err := cluster.DetectPlatform(t.Context(), tc.client, "", "redhat-ods-operator")
			if tc.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}

			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestDetectPlatform_OLMv1Absent(t *testing.T) {
	t.Parallel()

	noOLMv1 := &noOLMv1Client{
		Reader: fake.NewClientBuilder().WithScheme(runtime.NewScheme()).Build(),
	}

	result, err := cluster.DetectPlatform(t.Context(), noOLMv1, "", "redhat-ods-operator")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if result != cluster.OpenDataHub {
		t.Errorf("DetectPlatform() = %q, want %q", result, cluster.OpenDataHub)
	}
}

type noOLMv1Client struct {
	client.Reader
}

func (c *noOLMv1Client) Get(
	ctx context.Context, key types.NamespacedName, obj client.Object, opts ...client.GetOption,
) error {
	catalogGVK := testClusterCatalogGVK()
	if obj.GetObjectKind().GroupVersionKind() == catalogGVK {
		return &meta.NoKindMatchError{
			GroupKind: schema.GroupKind{Group: catalogGVK.Group, Kind: catalogGVK.Kind},
		}
	}

	return c.Reader.Get(ctx, key, obj, opts...)
}

func (c *noOLMv1Client) List(
	ctx context.Context, list client.ObjectList, opts ...client.ListOption,
) error {
	extensionGVK := testClusterExtensionGVK()
	if list.GetObjectKind().GroupVersionKind() == extensionGVK {
		return &meta.NoKindMatchError{
			GroupKind: schema.GroupKind{Group: extensionGVK.Group, Kind: extensionGVK.Kind},
		}
	}

	return c.Reader.List(ctx, list, opts...)
}

// --- helpers ---

func newCatalogSource(name, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v1alpha1",
			"kind":       "CatalogSource",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
		},
	}
}

func newOperatorCondition(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "operators.coreos.com/v2",
			"kind":       "OperatorCondition",
			"metadata": map[string]any{
				"name": name,
			},
		},
	}
}

func newClusterCatalog(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "olm.operatorframework.io/v1",
			"kind":       "ClusterCatalog",
			"metadata": map[string]any{
				"name": name,
			},
		},
	}
}

func newClusterExtension(name, packageName, namespace string) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "olm.operatorframework.io/v1",
			"kind":       "ClusterExtension",
			"metadata": map[string]any{
				"name": name,
			},
			"spec": map[string]any{
				"namespace": namespace,
				"source": map[string]any{
					"sourceType": "Catalog",
					"catalog": map[string]any{
						"packageName": packageName,
					},
				},
			},
		},
	}
}
