// Package diag turns raw field logs into reports a person can act on. The first is the connection report: how well
// one robot stayed connected through one match.
package diag

import (
	"math"
	"sort"
)

// Sample is one row of a team's match log, taken every half second during a match.
type Sample struct {
	MatchTimeSec      float64
	DsLinked          bool
	RadioLinked       bool
	RioLinked         bool
	RobotLinked       bool
	Enabled           bool
	BatteryVoltage    float64
	MissedPacketCount int     // The driver station's running count of packets lost to the robot.
	TripTimeMs        int     // Driver station to robot and back.
	RxRate            float64 // Access point link rate to the robot's radio, Mbit/s; negative when unknown.
	TxRate            float64
	SignalNoiseRatio  int // dB; negative when unknown.
}

// Outage is a stretch of the match during which a link was down.
type Outage struct {
	Link     string  `json:"link"` // "driver station" or "robot".
	StartSec float64 `json:"startSec"`
	EndSec   float64 `json:"endSec"`
}

// DurationSec is how long the outage lasted.
func (o Outage) DurationSec() float64 {
	return o.EndSec - o.StartSec
}

// Stats summarises one measurement over the match.
type Stats struct {
	Count   int     `json:"count"`
	Min     float64 `json:"min"`
	Average float64 `json:"average"`
	P95     float64 `json:"p95"`
	Max     float64 `json:"max"`
}

func statsOf(values []float64) Stats {
	if len(values) == 0 {
		return Stats{}
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	sum := 0.0
	for _, value := range sorted {
		sum += value
	}
	return Stats{
		Count: len(sorted), Min: sorted[0], Average: sum / float64(len(sorted)),
		P95: sorted[int(math.Ceil(0.95*float64(len(sorted))))-1], Max: sorted[len(sorted)-1],
	}
}

// Thresholds at which the report calls something out. They are guidance for a person, not rules.
const (
	// The driver station talks to the robot 50 times a second.
	packetsPerSecond = 50.0
	// Below this the roboRIO may brown out, disabling outputs.
	BrownoutRiskVolts = 7.0
	// Loss above this shows as lag on the field.
	LossWatchPercent = 5.0
	// A round trip above this is felt by drivers.
	TripWatchMs = 50.0
	// Below this the radio link is marginal.
	SignalWatchDb = 20
)

// ConnectionReport is how well one robot stayed connected through one match.
type ConnectionReport struct {
	Samples           int      `json:"samples"`
	DurationSec       float64  `json:"durationSec"`
	LostPackets       int      `json:"lostPackets"`
	LossPercent       float64  `json:"lossPercent"` // Estimated, from 50 packets a second.
	TripTimeMs        Stats    `json:"tripTimeMs"`
	BatteryVolts      Stats    `json:"batteryVolts"`
	LowBatterySamples int      `json:"lowBatterySamples"` // Samples below BrownoutRiskVolts with the robot linked.
	LowestBatterySec  float64  `json:"lowestBatterySec"`  // When the battery was lowest.
	RxRateMbps        Stats    `json:"rxRateMbps"`
	TxRateMbps        Stats    `json:"txRateMbps"`
	SignalNoiseDb     Stats    `json:"signalNoiseDb"`
	Outages           []Outage `json:"outages"`
	RobotDownSec      float64  `json:"robotDownSec"`
	Verdict           string   `json:"verdict"` // "good", "watch" or "poor".
	Findings          []string `json:"findings"`
}

// AnalyzeConnection builds the report from a match log's samples, in time order.
func AnalyzeConnection(samples []Sample) *ConnectionReport {
	report := &ConnectionReport{Samples: len(samples), Outages: []Outage{}, Findings: []string{}}
	if len(samples) == 0 {
		report.Verdict = "poor"
		report.Findings = append(report.Findings, "No samples were logged for this robot in this match.")
		return report
	}
	report.DurationSec = samples[len(samples)-1].MatchTimeSec - samples[0].MatchTimeSec

	var trips, volts, rx, tx, snr []float64
	lowest := math.Inf(1)
	for i, sample := range samples {
		// The lost-packet count only goes up, except when the driver station restarts it; count only increases.
		if i > 0 && sample.MissedPacketCount > samples[i-1].MissedPacketCount {
			report.LostPackets += sample.MissedPacketCount - samples[i-1].MissedPacketCount
		}
		if sample.RobotLinked {
			trips = append(trips, float64(sample.TripTimeMs))
			if sample.BatteryVoltage > 0 {
				volts = append(volts, sample.BatteryVoltage)
				if sample.BatteryVoltage < BrownoutRiskVolts {
					report.LowBatterySamples++
				}
				if sample.BatteryVoltage < lowest {
					lowest = sample.BatteryVoltage
					report.LowestBatterySec = sample.MatchTimeSec
				}
			}
		}
		if sample.RxRate >= 0 {
			rx = append(rx, sample.RxRate)
		}
		if sample.TxRate >= 0 {
			tx = append(tx, sample.TxRate)
		}
		if sample.SignalNoiseRatio >= 0 {
			snr = append(snr, float64(sample.SignalNoiseRatio))
		}
	}
	if report.DurationSec > 0 {
		report.LossPercent = 100 * float64(report.LostPackets) / (report.DurationSec * packetsPerSecond)
	}
	report.TripTimeMs = statsOf(trips)
	report.BatteryVolts = statsOf(volts)
	report.RxRateMbps = statsOf(rx)
	report.TxRateMbps = statsOf(tx)
	report.SignalNoiseDb = statsOf(snr)

	report.Outages = append(report.Outages, outages(samples, "driver station", func(s Sample) bool {
		return s.DsLinked
	})...)
	robotOutages := outages(samples, "robot", func(s Sample) bool { return s.RobotLinked })
	report.Outages = append(report.Outages, robotOutages...)
	sort.SliceStable(report.Outages, func(a, b int) bool {
		return report.Outages[a].StartSec < report.Outages[b].StartSec
	})
	for _, outage := range robotOutages {
		report.RobotDownSec += outage.DurationSec()
	}

	report.judge()
	return report
}

// outages finds the stretches where a link was down. A stretch is open from the first sample showing it down to the
// first sample showing it up again; one still open at the end lasts to the last sample.
func outages(samples []Sample, link string, up func(Sample) bool) []Outage {
	var found []Outage
	var open *Outage
	for _, sample := range samples {
		if !up(sample) {
			if open == nil {
				open = &Outage{Link: link, StartSec: sample.MatchTimeSec}
			}
			open.EndSec = sample.MatchTimeSec
			continue
		}
		if open != nil {
			open.EndSec = sample.MatchTimeSec
			found = append(found, *open)
			open = nil
		}
	}
	if open != nil {
		found = append(found, *open)
	}
	return found
}

// judge sets the verdict and the findings worth telling a team about.
func (report *ConnectionReport) judge() {
	report.Verdict = "good"
	poor := func(finding string) {
		report.Verdict = "poor"
		report.Findings = append(report.Findings, finding)
	}
	watch := func(finding string) {
		if report.Verdict == "good" {
			report.Verdict = "watch"
		}
		report.Findings = append(report.Findings, finding)
	}

	if report.BatteryVolts.Count == 0 {
		poor("The robot never connected during the match.")
		return
	}
	if report.RobotDownSec >= 1 {
		count := 0
		for _, outage := range report.Outages {
			if outage.Link == "robot" {
				count++
			}
		}
		poor(formatFinding("The robot lost its connection %d time(s), for %.1f s in total.", count,
			report.RobotDownSec))
	}
	if report.LowBatterySamples > 0 {
		watch(formatFinding("The battery fell below %.1f V (lowest %.2f V, %.1f s into the match): brownout risk.",
			BrownoutRiskVolts, report.BatteryVolts.Min, report.LowestBatterySec))
	}
	if report.LossPercent > LossWatchPercent {
		watch(formatFinding("About %.1f%% of packets to the robot were lost.", report.LossPercent))
	}
	if report.TripTimeMs.P95 > TripWatchMs {
		watch(formatFinding("Round trips were slow: 95%% under %.0f ms, worst %.0f ms.", report.TripTimeMs.P95,
			report.TripTimeMs.Max))
	}
	if report.SignalNoiseDb.Count > 0 && report.SignalNoiseDb.Min < SignalWatchDb {
		watch(formatFinding("The radio signal was weak at times (lowest %.0f dB signal to noise).",
			report.SignalNoiseDb.Min))
	}
}
