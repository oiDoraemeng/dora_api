package handler

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestRequireExplicitImagesModel(t *testing.T) {
	for _, tc := range []struct {
		name    string
		request *service.OpenAIImagesRequest
		wantErr string
	}{
		{name: "missing request", wantErr: "model is required"},
		{name: "defaulted model", request: &service.OpenAIImagesRequest{Model: "gpt-image-2"}, wantErr: "model is required"},
		{name: "explicit model", request: &service.OpenAIImagesRequest{Model: "gpt-image-2", ExplicitModel: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := requireExplicitImagesModel(tc.request)
			if tc.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.EqualError(t, err, tc.wantErr)
		})
	}
}
