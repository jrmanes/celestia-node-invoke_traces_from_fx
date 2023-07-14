package nodebuilder

import (
	"context"
	"fmt"
	"time"

	"github.com/libp2p/go-libp2p/core/peer"
	"github.com/pyroscope-io/client/pyroscope"
	otelpyroscope "github.com/pyroscope-io/otel-profiling-go"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlpmetric/otlpmetrichttp"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	sdk "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/resource"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.11.0"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/fx"

	"github.com/celestiaorg/go-fraud"

	"github.com/celestiaorg/celestia-node/nodebuilder/das"
	modheader "github.com/celestiaorg/celestia-node/nodebuilder/header"
	"github.com/celestiaorg/celestia-node/nodebuilder/node"
	"github.com/celestiaorg/celestia-node/nodebuilder/p2p"
	"github.com/celestiaorg/celestia-node/nodebuilder/share"
	"github.com/celestiaorg/celestia-node/state"
)

// WithNetwork specifies the Network to which the Node should connect to.
// WARNING: Use this option with caution and never run the Node with different networks over the
// same persisted Store.
func WithNetwork(net p2p.Network) fx.Option {
	return fx.Replace(net)
}

// WithBootstrappers sets custom bootstrap peers.
func WithBootstrappers(peers p2p.Bootstrappers) fx.Option {
	return fx.Replace(peers)
}

// WithPyroscope enables pyroscope profiling for the node.
func WithPyroscope(endpoint string, nodeType node.Type) fx.Option {
	return fx.Options(
		fx.Invoke(func(peerID peer.ID) error {
			_, err := pyroscope.Start(pyroscope.Config{
				ApplicationName: "celestia.da-node",
				ServerAddress:   endpoint,
				Tags: map[string]string{
					"type":   nodeType.String(),
					"peerId": peerID.String(),
				},
				Logger: nil,
				ProfileTypes: []pyroscope.ProfileType{
					pyroscope.ProfileCPU,
					pyroscope.ProfileAllocObjects,
					pyroscope.ProfileAllocSpace,
					pyroscope.ProfileInuseObjects,
					pyroscope.ProfileInuseSpace,
				},
			})
			return err
		}),
	)
}

// WithMetrics enables metrics exporting for the node.
func WithMetrics(metricOpts []otlpmetrichttp.Option, nodeType node.Type, buildInfo *node.BuildInfo) fx.Option {
	baseComponents := fx.Options(
		fx.Supply(metricOpts),
		fx.Supply(buildInfo),
		fx.Invoke(initializeMetrics),
		fx.Invoke(state.WithMetrics),
		fx.Invoke(fraud.WithMetrics),
		fx.Invoke(node.WithMetrics),
		fx.Invoke(modheader.WithMetrics),
		fx.Invoke(share.WithDiscoveryMetrics),
	)

	samplingMetrics := fx.Options(
		fx.Invoke(das.WithMetrics),
		fx.Invoke(share.WithPeerManagerMetrics),
		fx.Invoke(share.WithShrexClientMetrics),
		fx.Invoke(share.WithShrexGetterMetrics),
	)

	var opts fx.Option
	switch nodeType {
	case node.Full:
		opts = fx.Options(
			baseComponents,
			fx.Invoke(share.WithShrexServerMetrics),
			samplingMetrics,
		)
	case node.Light:
		opts = fx.Options(
			baseComponents,
			samplingMetrics,
		)
	case node.Bridge:
		opts = fx.Options(
			baseComponents,
			fx.Invoke(share.WithShrexServerMetrics),
		)
	default:
		panic("invalid node type")
	}
	return opts
}

func WithTraces(opts []otlptracehttp.Option, pyroOpts []otelpyroscope.Option) fx.Option {
	options := fx.Options(
		fx.Supply(opts),
		fx.Supply(pyroOpts),
		fx.Invoke(initializeTraces),
	)
	return options
}

func initializeTraces(
	ctx context.Context,
	nodeType node.Type,
	peerID peer.ID,
	network p2p.Network,
	buildInfo *node.BuildInfo,
	opts []otlptracehttp.Option,
	pyroOpts []otelpyroscope.Option,
) error {
	var tp trace.TracerProvider
	client := otlptracehttp.NewClient(opts...)
	exporter, err := otlptrace.New(ctx, client)
	if err != nil {
		return fmt.Errorf("creating OTLP trace exporter: %w", err)
	}

	tp = tracesdk.NewTracerProvider(
		tracesdk.WithSampler(tracesdk.AlwaysSample()),
		// Always be sure to batch in production.
		tracesdk.WithBatcher(exporter),
		// Record information about this application in a Resource.
		tracesdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNamespaceKey.String(fmt.Sprintf("Celestia-%s", nodeType)),
			semconv.ServiceNameKey.String(fmt.Sprintf("semver-%s", buildInfo.SemanticVersion)),
			semconv.ServiceInstanceIDKey.String(fmt.Sprintf("%s/%s", network.String(), peerID.String()))),
		))

	if len(pyroOpts) > 0 {
		tp = otelpyroscope.NewTracerProvider(tp, pyroOpts...)
	}
	otel.SetTracerProvider(tp)
	return nil
}

// initializeMetrics initializes the global meter provider.
func initializeMetrics(
	ctx context.Context,
	lc fx.Lifecycle,
	peerID peer.ID,
	nodeType node.Type,
	buildInfo *node.BuildInfo,
	network p2p.Network,
	opts []otlpmetrichttp.Option,
) error {
	exp, err := otlpmetrichttp.New(ctx, opts...)
	if err != nil {
		return err
	}

	provider := sdk.NewMeterProvider(
		sdk.WithReader(sdk.NewPeriodicReader(exp, sdk.WithTimeout(2*time.Second))),
		sdk.WithResource(resource.NewWithAttributes(
			semconv.SchemaURL,
			semconv.ServiceNamespaceKey.String(fmt.Sprintf("Celestia-%s", nodeType.String())),
			semconv.ServiceNameKey.String(fmt.Sprintf("semver-%s", buildInfo.SemanticVersion)),
			semconv.ServiceInstanceIDKey.String(fmt.Sprintf("%s/%s", network.String(), peerID.String())))))
	lc.Append(fx.Hook{
		OnStop: func(ctx context.Context) error {
			return provider.Shutdown(ctx)
		},
	})
	otel.SetMeterProvider(provider)
	return nil
}
