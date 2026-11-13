package diag

import (
	"github.com/stretchr/testify/assert"
	"testing"
)

// steadyMatch returns samples every half second for the given number of seconds with a healthy robot.
func steadyMatch(seconds float64) []Sample {
	var samples []Sample
	for t := 0.0; t <= seconds; t += 0.5 {
		samples = append(samples, Sample{
			MatchTimeSec: t, DsLinked: true, RadioLinked: true, RioLinked: true, RobotLinked: true, Enabled: true,
			BatteryVoltage: 12.4 - t/100, TripTimeMs: 4, RxRate: 433, TxRate: 400, SignalNoiseRatio: 38,
		})
	}
	return samples
}

func TestHealthyRobot(t *testing.T) {
	report := AnalyzeConnection(steadyMatch(150))
	assert.Equal(t, "good", report.Verdict)
	assert.Empty(t, report.Findings)
	assert.Empty(t, report.Outages)
	assert.Equal(t, 301, report.Samples)
	assert.Equal(t, 150.0, report.DurationSec)
	assert.Equal(t, 0.0, report.LossPercent)
	assert.Equal(t, 4.0, report.TripTimeMs.P95)
	assert.InDelta(t, 10.9, report.BatteryVolts.Min, 1e-9)
	assert.Equal(t, 150.0, report.LowestBatterySec)
	assert.Equal(t, 433.0, report.RxRateMbps.Average)
}

func TestDisconnectsAndProblems(t *testing.T) {
	samples := steadyMatch(150)
	// The robot drops out from 40 s to 43 s, then from 149.5 s to the end.
	for i := range samples {
		t := samples[i].MatchTimeSec
		if (t >= 40 && t < 43) || t >= 149.5 {
			samples[i].RobotLinked = false
			samples[i].RioLinked = false
		}
		// A sag below 7 V at 90 s, slow trips late, packet loss growing steadily, a weak signal for a while.
		if t == 90 {
			samples[i].BatteryVoltage = 6.6
		}
		if t > 120 {
			samples[i].TripTimeMs = 80
		}
		samples[i].MissedPacketCount = int(t * 4)
		if t >= 60 && t < 70 {
			samples[i].SignalNoiseRatio = 12
		}
	}
	// The driver station restarts its loss count once; only increases count.
	samples[200].MissedPacketCount = 0

	report := AnalyzeConnection(samples)
	assert.Equal(t, "poor", report.Verdict)
	if assert.Len(t, report.Outages, 2) {
		assert.Equal(t, Outage{Link: "robot", StartSec: 40, EndSec: 43}, report.Outages[0])
		assert.Equal(t, Outage{Link: "robot", StartSec: 149.5, EndSec: 150}, report.Outages[1])
	}
	assert.InDelta(t, 3.5, report.RobotDownSec, 1e-9)
	assert.Equal(t, 1, report.LowBatterySamples)
	assert.Equal(t, 6.6, report.BatteryVolts.Min)
	assert.Equal(t, 90.0, report.LowestBatterySec)
	assert.Greater(t, report.LossPercent, LossWatchPercent)
	assert.Equal(t, 80.0, report.TripTimeMs.P95)
	assert.Equal(t, 12.0, report.SignalNoiseDb.Min)
	assert.Len(t, report.Findings, 5)
	assert.Contains(t, report.Findings[0], "lost its connection 2 time(s), for 3.5 s")
	assert.Contains(t, report.Findings[1], "lowest 6.60 V, 90.0 s into the match")
}

func TestRobotNeverConnected(t *testing.T) {
	samples := steadyMatch(10)
	for i := range samples {
		samples[i].RobotLinked = false
		samples[i].BatteryVoltage = 0
		samples[i].RxRate, samples[i].TxRate, samples[i].SignalNoiseRatio = -1, -1, -1
	}
	report := AnalyzeConnection(samples)
	assert.Equal(t, "poor", report.Verdict)
	assert.Equal(t, []string{"The robot never connected during the match."}, report.Findings)
	assert.Equal(t, 0, report.RxRateMbps.Count)

	empty := AnalyzeConnection(nil)
	assert.Equal(t, "poor", empty.Verdict)
	assert.Equal(t, 0, empty.Samples)
}
