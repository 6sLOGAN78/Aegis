package control_test

import (
	"context"
	"testing"
	"time"

	"aegis/internal/telemetry"
	snapshotv1 "aegis/pkg/api/snapshot/v1"

	"github.com/stretchr/testify/require"
)

func metricValue(t *testing.T, m *telemetry.Metrics, name string) float64 {
	t.Helper()
	mfs, err := m.Registry.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, mm := range mf.GetMetric() {
			if mm.Gauge != nil {
				return mm.GetGauge().GetValue()
			}
			if mm.Counter != nil {
				return mm.GetCounter().GetValue()
			}
		}
	}
	t.Fatalf("metric %s not found", name)
	return 0
}

func TestControl_ConnectedGatewaysGauge(t *testing.T) {
	distServer, _, _, _, client, cleanup := setupTestControlPlane(t)
	defer cleanup()

	m := telemetry.NewMetrics()
	distServer.SetMetrics(m)

	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel1()
	ctx2, cancel2 := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel2()

	s1, err := client.StreamSnapshots(ctx1)
	require.NoError(t, err)
	_, err = s1.Recv() // initial snapshot proves the server registered the client
	require.NoError(t, err)
	s2, err := client.StreamSnapshots(ctx2)
	require.NoError(t, err)
	_, err = s2.Recv()
	require.NoError(t, err)

	require.Eventually(t, func() bool {
		return metricValue(t, m, "aegis_control_plane_connected_gateways") == 2
	}, 5*time.Second, 10*time.Millisecond)

	cancel1()
	require.Eventually(t, func() bool {
		return metricValue(t, m, "aegis_control_plane_connected_gateways") == 1
	}, 5*time.Second, 10*time.Millisecond)
}

func TestControl_PublishMetricIncrementsOnBroadcast(t *testing.T) {
	distServer, _, signer, _, _, cleanup := setupTestControlPlane(t)
	defer cleanup()

	m := telemetry.NewMetrics()
	distServer.SetMetrics(m)

	env, err := signer.SignSnapshot(&snapshotv1.SnapshotPayload{Version: 2})
	require.NoError(t, err)

	distServer.BroadcastSnapshot(env)
	distServer.BroadcastSnapshot(env)
	require.Equal(t, 2.0, metricValue(t, m, "aegis_control_plane_snapshot_publish_total"))
}

func TestControl_PublishMetricNilMetricsSafe(t *testing.T) {
	distServer, _, signer, _, _, cleanup := setupTestControlPlane(t)
	defer cleanup()

	env, err := signer.SignSnapshot(&snapshotv1.SnapshotPayload{Version: 2})
	require.NoError(t, err)
	require.NotPanics(t, func() { distServer.BroadcastSnapshot(env) })
}
