package controller

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	kstatus "github.com/fluxcd/cli-utils/pkg/kstatus/status"
	kcv1 "github.com/fluxcd/kustomize-controller/api/v1"
	gotkmeta "github.com/fluxcd/pkg/apis/meta"
	gotkstorage "github.com/fluxcd/pkg/artifact/storage"
	"github.com/fluxcd/pkg/runtime/patch"
	scv1 "github.com/fluxcd/source-controller/api/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

const (
	ManagedByLabelKey = "app.kubernetes.io/managed-by"
	ControllerName    = "flux-bridge"

	dataFileName = "data.yaml"
	pollInterval = 2 * time.Second
)

type FluxController struct {
	kubeClient client.Client
	namespace  string
	storage    *gotkstorage.Storage
}

func NewFluxController(ctx context.Context, storage *gotkstorage.Storage, kubeClient client.Client, namespace string) (FluxController, error) {
	err := addToScheme(kubeClient.Scheme())
	if err != nil {
		return FluxController{}, err
	}
	kustList := kcv1.KustomizationList{}
	err = kubeClient.List(ctx, &kustList, client.InNamespace(namespace))
	if err != nil {
		return FluxController{}, err
	}
	eaList := scv1.ExternalArtifactList{}
	err = kubeClient.List(ctx, &eaList, client.InNamespace(namespace))
	if err != nil {
		return FluxController{}, err
	}

	client := FluxController{
		kubeClient: kubeClient,
		namespace:  namespace,
		storage:    storage,
	}
	return client, nil
}

func (c FluxController) Diff(ctx context.Context, name string, data []byte) (bool, string, error) {
	kust := kcv1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels: map[string]string{
				ManagedByLabelKey: ControllerName,
			},
		},
	}
	err := c.kubeClient.Get(ctx, client.ObjectKeyFromObject(&kust), &kust)
	if apierrors.IsNotFound(err) {
		return true, fmt.Sprintf("Kustomization %s could not be found", name), nil
	}
	if err != nil {
		return false, "", err
	}

	ea := scv1.ExternalArtifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
		},
	}
	err = c.kubeClient.Get(ctx, client.ObjectKeyFromObject(&ea), &ea)
	if apierrors.IsNotFound(err) {
		return true, fmt.Sprintf("External Artifact %s could not be found", name), nil
	}
	if err != nil {
		return false, "", err
	}
	if ea.Status.Artifact == nil {
		return true, "External Artifact status is empty", nil
	}
	if !c.storage.ArtifactExist(*ea.Status.Artifact) {
		return true, "Artifact does not exist on disk", nil
	}
	if ea.Status.Artifact.Revision != kust.Status.LastAppliedRevision {
		return true, "External Artifact revision does not match Kustomization last applied revision", nil
	}

	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return false, "", err
	}
	defer os.RemoveAll(tmpDir)
	err = c.storage.CopyToPath(ea.Status.Artifact, dataFileName, filepath.Join(tmpDir, dataFileName))
	if err != nil {
		return false, "", err
	}
	current, err := os.ReadFile(filepath.Join(tmpDir, dataFileName))
	if err != nil {
		return false, "", err
	}
	if !bytes.Equal(current, data) {
		return true, "External Artifact current data does not match expected data", nil
	}
	return false, "No drift detected", nil
}

// Apply will apply the given configuration using Flux.
func (c FluxController) Apply(ctx context.Context, name string, revision string, data []byte) error {
	if len(data) == 0 {
		return errors.New("can't apply empty data")
	}
	if name == "" {
		return errors.New("name can't be empty")
	}
	if revision == "" {
		return errors.New("revision can't be empty")
	}

	ea := scv1.ExternalArtifact{
		TypeMeta: metav1.TypeMeta{
			APIVersion: scv1.GroupVersion.String(),
			Kind:       scv1.ExternalArtifactKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels: map[string]string{
				ManagedByLabelKey: ControllerName,
			},
		},
		Spec:   scv1.ExternalArtifactSpec{},
		Status: scv1.ExternalArtifactStatus{},
	}

	// Write artifact contents.
	tmpDir, err := os.MkdirTemp("", "")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)
	err = os.WriteFile(filepath.Join(tmpDir, dataFileName), data, 0o644)
	if err != nil {
		return err
	}
	artifact := c.storage.NewArtifactFor("confighub", &ea.ObjectMeta, revision, fmt.Sprintf("%s.tar.gz", revision))
	err = c.storage.MkdirAll(artifact)
	if err != nil {
		return err
	}
	err = c.storage.Archive(&artifact, tmpDir, nil)
	if err != nil {
		return err
	}

	// Patch external artifact and status.
	err = c.kubeClient.Patch(ctx, &ea, client.Apply, &client.PatchOptions{
		FieldManager: ControllerName,
		Force:        ptr.To(true),
	})
	if err != nil {
		return err
	}

	ea.TypeMeta = metav1.TypeMeta{
		APIVersion: scv1.GroupVersion.String(),
		Kind:       scv1.ExternalArtifactKind,
	}
	ea.ManagedFields = nil
	ea.Status = scv1.ExternalArtifactStatus{
		Artifact: &artifact,
		Conditions: []metav1.Condition{
			{
				ObservedGeneration: ea.GetGeneration(),
				Type:               gotkmeta.ReadyCondition,
				Status:             metav1.ConditionTrue,
				LastTransitionTime: metav1.Now(),
				Reason:             gotkmeta.SucceededReason,
				Message:            "Artifact is ready",
			},
		},
	}
	statusOpts := &client.SubResourcePatchOptions{
		PatchOptions: client.PatchOptions{
			FieldManager: ControllerName,
		},
	}
	err = c.kubeClient.Status().Patch(ctx, &ea, client.Apply, statusOpts)
	if err != nil {
		return err
	}

	// Apply the Kustomization deploying from the External Artifact.
	kust := kcv1.Kustomization{
		TypeMeta: metav1.TypeMeta{
			APIVersion: kcv1.GroupVersion.String(),
			Kind:       kcv1.KustomizationKind,
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
			Labels: map[string]string{
				ManagedByLabelKey: ControllerName,
			},
		},
		Spec: kcv1.KustomizationSpec{
			Interval: metav1.Duration{Duration: 1 * time.Minute},
			Timeout:  &metav1.Duration{Duration: 5 * time.Minute},
			Wait:     true,
			Prune:    true,
			SourceRef: kcv1.CrossNamespaceSourceReference{
				Kind:      scv1.ExternalArtifactKind,
				Name:      ea.ObjectMeta.Name,
				Namespace: c.namespace,
			},
		},
	}
	err = c.kubeClient.Patch(ctx, &kust, client.Apply, &client.PatchOptions{
		FieldManager: ControllerName,
		Force:        ptr.To(true),
	})
	if err != nil {
		return err
	}
	err = waitForCurrentStatus(ctx, c.kubeClient, kust, revision)
	if err != nil {
		return err
	}

	// Garbage collect the old artifact.
	_, err = c.storage.GarbageCollect(ctx, *ea.Status.Artifact, 5*time.Second)
	if err != nil {
		return err
	}

	return nil
}

func (c *FluxController) Delete(ctx context.Context, name string) error {
	kust := kcv1.Kustomization{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
		},
	}
	err := c.kubeClient.Delete(ctx, &kust)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	err = wait.PollUntilContextCancel(ctx, pollInterval, true, func(ctx context.Context) (bool, error) {
		err = c.kubeClient.Get(ctx, client.ObjectKeyFromObject(&kust), &kust)
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		if err != nil {
			return false, err
		}
		return false, nil
	})

	ea := scv1.ExternalArtifact{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: c.namespace,
		},
	}
	err = c.kubeClient.Get(ctx, client.ObjectKeyFromObject(&ea), &ea)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}
	err = c.kubeClient.Delete(ctx, &ea)
	if err != nil && !apierrors.IsNotFound(err) {
		return err
	}

	if ea.Status.Artifact != nil {
		_, err := c.storage.RemoveAll(*ea.Status.Artifact)
		if err != nil {
			return err
		}
	}
	return nil
}

func addToScheme(sc *runtime.Scheme) error {
	err := scv1.AddToScheme(sc)
	if err != nil {
		return err
	}
	err = kcv1.AddToScheme(sc)
	if err != nil {
		return err
	}
	err = scv1.AddToScheme(sc)
	if err != nil {
		return err
	}
	return nil
}

func waitForCurrentStatus(ctx context.Context, kubeClient client.Client, kust kcv1.Kustomization, revision string) error {
	waitCtx, waitCancel := context.WithTimeout(ctx, kust.Spec.Timeout.Duration)
	defer waitCancel()
	err := wait.PollUntilContextCancel(waitCtx, pollInterval, true, func(ctx context.Context) (bool, error) {
		err := kubeClient.Get(ctx, client.ObjectKeyFromObject(&kust), &kust)
		if err != nil {
			return false, err
		}
		if kust.Status.LastAttemptedRevision != "" && kust.Status.LastAttemptedRevision != revision {
			return false, nil
		}
		err = isStalled(kust)
		if err != nil {
			return false, err
		}
		u, err := patch.ToUnstructured(&kust)
		if err != nil {
			return false, err
		}
		result, err := kstatus.Compute(u)
		switch result.Status {
		case kstatus.CurrentStatus:
			return true, nil
		case kstatus.InProgressStatus:
			return false, nil
		case kstatus.UnknownStatus:
			return false, nil
		default:
			return false, wait.ErrorInterrupted(fmt.Errorf("failed Kustomization status %s", result.Status))
		}
	})
	if err != nil {
		return err
	}
	return nil
}

func isStalled(kust kcv1.Kustomization) error {
	for _, cond := range kust.Status.Conditions {
		if cond.Type == kstatus.ConditionStalled.String() && cond.Status == metav1.ConditionTrue {
			return errors.New(cond.Message)
		}
	}
	return nil
}
