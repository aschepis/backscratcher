package agent

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

// ScheduleParser wraps a cron schedule and provides Next() method
type ScheduleParser interface {
	Next(time.Time) time.Time
}

// cronSchedule wraps the cron.Schedule
type cronSchedule struct {
	schedule cron.Schedule
}

func (cs *cronSchedule) Next(t time.Time) time.Time {
	return cs.schedule.Next(t)
}

// ParseSchedule parses a schedule string and returns a ScheduleParser.
// Supports:
//   - Cron expressions: "0 */15 * * * *" (6-field) or "*/15 * * * *" (5-field)
//   - Go duration strings: "15m", "2h", "1h30m"
func ParseSchedule(schedule string) (ScheduleParser, error) {
	if schedule == "" {
		return nil, fmt.Errorf("schedule string is empty")
	}

	// Try parsing as cron expression first (supports both 5 and 6 field formats)
	// Use parser that accepts optional seconds
	parser := cron.NewParser(cron.SecondOptional | cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow | cron.Descriptor)
	cronSched, err := parser.Parse(schedule)
	if err == nil {
		return &cronSchedule{schedule: cronSched}, nil
	}

	// If cron parsing fails, try parsing as Go duration string
	duration, err := time.ParseDuration(schedule)
	if err != nil {
		return nil, fmt.Errorf("failed to parse schedule as cron expression or duration: %w", err)
	}

	// Convert duration to ConstantDelaySchedule
	constantSchedule := cron.ConstantDelaySchedule{Delay: duration}
	return &cronSchedule{schedule: constantSchedule}, nil
}

// ComputeNextWake computes the next wake time from a schedule string given a base time.
func ComputeNextWake(schedule string, baseTime time.Time) (time.Time, error) {
	parser, err := ParseSchedule(schedule)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to parse schedule %q: %w", schedule, err)
	}

	return parser.Next(baseTime), nil
}
