package monitoring

import (
	"fmt"
	"time"
)

// ReportGenerator generates performance reports
type ReportGenerator struct{}

// NewReportGenerator creates a new report generator
func NewReportGenerator() *ReportGenerator {
	return &ReportGenerator{}
}

// Generate creates a performance report from metrics
func (rg *ReportGenerator) Generate(metrics *PerformanceMetrics) *Report {
	report := &Report{
		Metrics:         metrics,
		Recommendations: []string{},
		Warnings:        []string{},
		GeneratedAt:     time.Now(),
		Period:          metrics.Period,
	}

	// Analyze performance and generate recommendations
	rg.analyzeWinRate(metrics, report)
	rg.analyzeProfitFactor(metrics, report)
	rg.analyzeRiskReward(metrics, report)

	return report
}

// analyzeWinRate analyzes win rate and provides recommendations
func (rg *ReportGenerator) analyzeWinRate(metrics *PerformanceMetrics, report *Report) {
	if metrics.ExecutedTrades < 10 {
		report.Warnings = append(report.Warnings,
			"⚠️ Insufficient data: Less than 10 trades completed")
		return
	}

	if metrics.WinRate < 0.35 {
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("🚨 Low win rate: %.1f%% (target: >35%%)", metrics.WinRate*100))
		report.Recommendations = append(report.Recommendations,
			"Consider reviewing entry criteria and confluence requirements")
	} else if metrics.WinRate > 0.60 {
		report.Recommendations = append(report.Recommendations,
			fmt.Sprintf("✅ Excellent win rate: %.1f%%", metrics.WinRate*100))
	}
}

// analyzeProfitFactor analyzes profit factor
func (rg *ReportGenerator) analyzeProfitFactor(metrics *PerformanceMetrics, report *Report) {
	if metrics.ProfitFactor < 1.0 {
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("🚨 Losing system: Profit factor %.2f (target: >1.5)", metrics.ProfitFactor))
		report.Recommendations = append(report.Recommendations,
			"System is losing money. Review strategy and risk management")
	} else if metrics.ProfitFactor < 1.5 {
		report.Warnings = append(report.Warnings,
			fmt.Sprintf("⚠️ Low profit factor: %.2f (target: >1.5)", metrics.ProfitFactor))
		report.Recommendations = append(report.Recommendations,
			"Improve risk-reward ratios or reduce losing trades")
	} else if metrics.ProfitFactor > 2.0 {
		report.Recommendations = append(report.Recommendations,
			fmt.Sprintf("✅ Strong profit factor: %.2f", metrics.ProfitFactor))
	}
}

// analyzeRiskReward analyzes risk-reward metrics
func (rg *ReportGenerator) analyzeRiskReward(metrics *PerformanceMetrics, report *Report) {
	if metrics.AverageWin > 0 && metrics.AverageLoss < 0 {
		rr := metrics.AverageWin / (-metrics.AverageLoss)
		if rr < 1.5 {
			report.Warnings = append(report.Warnings,
				fmt.Sprintf("⚠️ Low avg R:R: %.2f (target: >1.5)", rr))
			report.Recommendations = append(report.Recommendations,
				"Increase take profit targets or tighten stop losses")
		} else if rr > 2.5 {
			report.Recommendations = append(report.Recommendations,
				fmt.Sprintf("✅ Excellent avg R:R: %.2f", rr))
		}
	}
}
