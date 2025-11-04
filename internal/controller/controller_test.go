package controller

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	kcv1 "github.com/fluxcd/kustomize-controller/api/v1"
	"github.com/fluxcd/pkg/artifact/config"
	gotkstorage "github.com/fluxcd/pkg/artifact/storage"
	scv1 "github.com/fluxcd/source-controller/api/v1"
	"github.com/stretchr/testify/require"
	kerrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/kubectl/pkg/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
)

func TestController(t *testing.T) {
	t.Parallel()

	namespace := "confighub"
	sc := scheme.Scheme
	err := addToScheme(sc)
	require.NoError(t, err)
	int := interceptor.Funcs{
		Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
			if ea, ok := obj.(*scv1.ExternalArtifact); ok {
				currentEa := scv1.ExternalArtifact{
					ObjectMeta: ea.ObjectMeta,
				}
				err := c.Get(ctx, client.ObjectKeyFromObject(&currentEa), &currentEa)
				if err != nil && !kerrors.IsNotFound(err) {
					return err
				}
				if err == nil {
					ea.Status = currentEa.Status
					obj = ea
				}
			}
			return c.Patch(ctx, obj, patch, opts...)
		},
		Get: func(ctx context.Context, c client.WithWatch, key client.ObjectKey, obj client.Object, opts ...client.GetOption) error {
			err = c.Get(ctx, key, obj, opts...)
			if err != nil {
				return err
			}
			if kust, ok := obj.(*kcv1.Kustomization); ok {
				ea := scv1.ExternalArtifact{
					ObjectMeta: kust.ObjectMeta,
				}
				err = c.Get(ctx, client.ObjectKeyFromObject(&ea), &ea)
				if err == nil {
					kust.Status.LastAttemptedRevision = ea.Status.Artifact.Revision
				}
			}
			return nil
		},
	}
	kubeClient := fake.NewClientBuilder().
		WithScheme(sc).
		WithStatusSubresource(&scv1.ExternalArtifact{}, &kcv1.Kustomization{}).
		WithInterceptorFuncs(int).
		Build()

	dataDir := t.TempDir()
	artifactCfg := &config.Options{
		StoragePath:    dataDir,
		StorageAddress: ":8080",
	}
	storage, err := gotkstorage.New(artifactCfg)
	require.NoError(t, err)
	ctrl, err := NewFluxController(t.Context(), storage, kubeClient, namespace)
	require.NoError(t, err)

	data := []byte("Hello World")
	name := "foo"
	version := "dev-1"

	// Initial create.
	err = ctrl.Apply(t.Context(), name, version, data)
	require.NoError(t, err)
	createdEa := scv1.ExternalArtifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err = kubeClient.Get(t.Context(), client.ObjectKeyFromObject(&createdEa), &createdEa)
	require.NoError(t, err)
	require.Equal(t, version, createdEa.Status.Artifact.Revision)
	require.Equal(t, "confighub/confighub/foo/dev-1.tar.gz", createdEa.Status.Artifact.Path)
	require.Equal(t, "http://localhost:8080/confighub/confighub/foo/dev-1.tar.gz", createdEa.Status.Artifact.URL)
	require.Equal(t, "sha256:4a651b6f42849f48ef468b2a691d19536d4d36f79246d7f341b2ba0db9ee8193", createdEa.Status.Artifact.Digest)

	tarData, err := os.ReadFile(filepath.Join(dataDir, createdEa.Status.Artifact.Path))
	require.NoError(t, err)
	require.Equal(t, int64(len(tarData)), *createdEa.Status.Artifact.Size)

	kust := kcv1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err = kubeClient.Get(t.Context(), client.ObjectKeyFromObject(&kust), &kust)
	require.NoError(t, err)
	require.True(t, kust.Spec.Wait)
	require.True(t, kust.Spec.Prune)
	expectedSourceRef := kcv1.CrossNamespaceSourceReference{
		Kind:      scv1.ExternalArtifactKind,
		Name:      name,
		Namespace: namespace,
	}
	require.Equal(t, expectedSourceRef, kust.Spec.SourceRef)

	// Configuration update.
	data = []byte("Hello World Updated")
	version = "dev-2"
	err = ctrl.Apply(t.Context(), name, version, data)
	require.NoError(t, err)
	updatedEa := scv1.ExternalArtifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: namespace,
		},
	}
	err = kubeClient.Get(t.Context(), client.ObjectKeyFromObject(&updatedEa), &updatedEa)
	require.NoError(t, err)
	_, err = os.Stat(filepath.Join(dataDir, createdEa.Status.Artifact.Path))
	require.ErrorIs(t, err, os.ErrNotExist)
	require.Equal(t, "sha256:25d419ea67c80580c6506127badae0bf630ccb73cf88e22732957b3093284358", updatedEa.Status.Artifact.Digest)

	// Diff current configuration.
	err = kubeClient.Get(t.Context(), client.ObjectKeyFromObject(&kust), &kust)
	require.NoError(t, err)
	kust.Status.LastAppliedRevision = updatedEa.Status.Artifact.Revision
	err = kubeClient.Status().Update(t.Context(), &kust)
	require.NoError(t, err)

	drift, msg, err := ctrl.Diff(t.Context(), name, data)
	require.NoError(t, err)
	require.Equal(t, "No drift detected", msg)
	require.False(t, drift)
}
