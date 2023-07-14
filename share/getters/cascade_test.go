package getters

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/golang/mock/gomock"
	"github.com/stretchr/testify/assert"

	"github.com/celestiaorg/rsmt2d"

	"github.com/celestiaorg/celestia-node/share"
	"github.com/celestiaorg/celestia-node/share/mocks"
)

func TestCascadeGetter(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	const gettersN = 3
	roots := make([]*share.Root, gettersN)
	getters := make([]share.Getter, gettersN)
	for i := range roots {
		getters[i], roots[i] = TestGetter(t)
	}

	getter := NewCascadeGetter(getters)
	t.Run("GetShare", func(t *testing.T) {
		for _, r := range roots {
			sh, err := getter.GetShare(ctx, r, 0, 0)
			assert.NoError(t, err)
			assert.NotEmpty(t, sh)
		}
	})

	t.Run("GetEDS", func(t *testing.T) {
		for _, r := range roots {
			sh, err := getter.GetEDS(ctx, r)
			assert.NoError(t, err)
			assert.NotEmpty(t, sh)
		}
	})
}

func TestCascade(t *testing.T) {
	ctrl := gomock.NewController(t)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	timeoutGetter := mocks.NewMockGetter(ctrl)
	immediateFailGetter := mocks.NewMockGetter(ctrl)
	successGetter := mocks.NewMockGetter(ctrl)
	ctxGetter := mocks.NewMockGetter(ctrl)
	timeoutGetter.EXPECT().GetEDS(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *share.Root) (*rsmt2d.ExtendedDataSquare, error) {
			return nil, context.DeadlineExceeded
		}).AnyTimes()
	immediateFailGetter.EXPECT().GetEDS(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("second getter fails immediately")).AnyTimes()
	successGetter.EXPECT().GetEDS(gomock.Any(), gomock.Any()).
		Return(nil, nil).AnyTimes()
	ctxGetter.EXPECT().GetEDS(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, _ *share.Root) (*rsmt2d.ExtendedDataSquare, error) {
			return nil, ctx.Err()
		}).AnyTimes()

	get := func(ctx context.Context, get share.Getter) (*rsmt2d.ExtendedDataSquare, error) {
		return get.GetEDS(ctx, nil)
	}

	t.Run("SuccessFirst", func(t *testing.T) {
		getters := []share.Getter{successGetter, timeoutGetter, immediateFailGetter}
		_, err := cascadeGetters(ctx, getters, get)
		assert.NoError(t, err)
	})

	t.Run("SuccessSecond", func(t *testing.T) {
		getters := []share.Getter{immediateFailGetter, successGetter}
		_, err := cascadeGetters(ctx, getters, get)
		assert.NoError(t, err)
	})

	t.Run("SuccessSecondAfterFirst", func(t *testing.T) {
		getters := []share.Getter{timeoutGetter, successGetter}
		_, err := cascadeGetters(ctx, getters, get)
		assert.NoError(t, err)
	})

	t.Run("SuccessAfterMultipleTimeouts", func(t *testing.T) {
		getters := []share.Getter{timeoutGetter, immediateFailGetter, timeoutGetter, timeoutGetter, successGetter}
		_, err := cascadeGetters(ctx, getters, get)
		assert.NoError(t, err)
	})

	t.Run("Error", func(t *testing.T) {
		getters := []share.Getter{immediateFailGetter, timeoutGetter, immediateFailGetter}
		_, err := cascadeGetters(ctx, getters, get)
		assert.Error(t, err)
		assert.Equal(t, strings.Count(err.Error(), "\n"), 2)
	})

	t.Run("Context Canceled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(ctx)
		cancel()
		getters := []share.Getter{ctxGetter, ctxGetter, ctxGetter}
		_, err := cascadeGetters(ctx, getters, get)
		assert.Error(t, err)
		assert.Equal(t, strings.Count(err.Error(), "\n"), 0)
	})

	t.Run("Single", func(t *testing.T) {
		getters := []share.Getter{successGetter}
		_, err := cascadeGetters(ctx, getters, get)
		assert.NoError(t, err)
	})
}
