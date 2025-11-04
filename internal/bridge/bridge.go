package bridge

import (
	"errors"
	"fmt"
	"strings"

	"github.com/confighub/sdk/bridge-worker/api"
	"github.com/confighub/sdk/bridge-worker/lib"
	"github.com/confighub/sdk/workerapi"
	"github.com/gosimple/slug"

	"github.com/confighubai/flux-bridge/internal/controller"
)

var _ api.Bridge = &FluxBridge{}

type FluxBridge struct {
	fluxCtrl controller.FluxController
	name     string
}

func NewFluxBridge(fluxCtrl controller.FluxController, name string) (*FluxBridge, error) {
	return &FluxBridge{
		fluxCtrl: fluxCtrl,
		name:     name,
	}, nil
}

func (b *FluxBridge) Info(opts api.InfoOptions) api.BridgeInfo {
	return api.BridgeInfo{
		SupportedConfigTypes: []*api.ConfigType{
			{
				ToolchainType: workerapi.ToolchainKubernetesYAML,
				ProviderType:  api.ProviderType("FluxExternalArtifact"),
				AvailableTargets: []api.Target{
					{
						Name: slug.Make(b.name),
					},
				},
			},
		},
	}
}

func (b *FluxBridge) Apply(wctx api.BridgeContext, payload api.BridgePayload) error {
	err := wctx.SendStatus(&api.ActionResult{
		ActionResultBaseMeta: api.ActionResultMeta{
			Status:  api.ActionStatusProgressing,
			Result:  api.ActionResultNone,
			Message: "Starting apply operation",
		},
	})
	if err != nil {
		return err
	}

	version := fmt.Sprintf("%d", payload.RevisionNum)
	err = b.fluxCtrl.Apply(wctx.Context(), payloadToName(payload), version, payload.Data)
	if err != nil {
		return lib.SafeSendStatus(wctx, &api.ActionResult{
			ActionResultBaseMeta: api.ActionResultMeta{
				Status:  api.ActionStatusFailed,
				Result:  api.ActionResultApplyFailed,
				Message: fmt.Sprintf("Flux controller apply error: %s", err.Error()),
			},
		}, err)
	}

	return wctx.SendStatus(&api.ActionResult{
		ActionResultBaseMeta: api.ActionResultMeta{
			Status:  api.ActionStatusCompleted,
			Result:  api.ActionResultApplyCompleted,
			Message: "Successfully completed apply operation",
		},
		LiveState: payload.Data,
	})
}

func (b *FluxBridge) Refresh(wctx api.BridgeContext, payload api.BridgePayload) error {
	err := wctx.SendStatus(&api.ActionResult{
		ActionResultBaseMeta: api.ActionResultMeta{
			Status:  api.ActionStatusProgressing,
			Result:  api.ActionResultNone,
			Message: "Starting refersh operation",
		},
	})
	if err != nil {
		return err
	}

	drift, msg, err := b.fluxCtrl.Diff(wctx.Context(), payloadToName(payload), payload.Data)
	if err != nil {
		return lib.SafeSendStatus(wctx, &api.ActionResult{
			ActionResultBaseMeta: api.ActionResultMeta{
				Status:  api.ActionStatusFailed,
				Result:  api.ActionResultRefreshFailed,
				Message: fmt.Sprintf("Flux controller diff error: %s", err.Error()),
			},
		}, err)
	}

	result := api.ActionResultRefreshAndNoDrift
	if drift {
		result = api.ActionResultRefreshAndDrifted
	}
	return wctx.SendStatus(&api.ActionResult{
		ActionResultBaseMeta: api.ActionResultMeta{
			Status:  api.ActionStatusCompleted,
			Result:  result,
			Message: msg,
		},
		Data:      payload.Data,
		LiveState: payload.Data,
	})
}

func (b *FluxBridge) Import(ctx api.BridgeContext, payload api.BridgePayload) error {
	// TODO(phillebaba): Figure out in what scenario we would support imports.
	return errors.New("import not supported")
}

func (b *FluxBridge) Destroy(wctx api.BridgeContext, payload api.BridgePayload) error {
	err := wctx.SendStatus(&api.ActionResult{
		ActionResultBaseMeta: api.ActionResultMeta{
			Status:  api.ActionStatusProgressing,
			Result:  api.ActionResultNone,
			Message: fmt.Sprintf("Starting destroy operation for %s", b.name),
		},
	})
	if err != nil {
		return err
	}

	err = b.fluxCtrl.Delete(wctx.Context(), payloadToName(payload))
	if err != nil {
		return lib.SafeSendStatus(wctx, &api.ActionResult{
			ActionResultBaseMeta: api.ActionResultMeta{
				Status:  api.ActionStatusFailed,
				Result:  api.ActionResultDestroyFailed,
				Message: fmt.Sprintf("Flux controller destroy error: %s", err.Error()),
			},
		}, err)
	}

	return wctx.SendStatus(&api.ActionResult{
		ActionResultBaseMeta: api.ActionResultMeta{
			Result:  api.ActionResultDestroyCompleted,
			Status:  api.ActionStatusCompleted,
			Message: "Destroy operation completed",
		},
		Data:      payload.Data,
		LiveState: []byte{},
	})
}

func (b *FluxBridge) Finalize(ctx api.BridgeContext, payload api.BridgePayload) error {
	// TODO(phillebaba): Verify if anything needs to be done during finalize.
	return nil
}

func payloadToName(payload api.BridgeWorkerPayload) string {
	return strings.Join([]string{payload.SpaceSlug, payload.UnitSlug}, "-")
}
