package model

import (
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

var workflowCronParser = cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)

// WorkflowSchedule parses the schedule configuration used by workflows and
// provisioning, without carrying the installation's last scheduled occurrence.
func WorkflowSchedule(expression, zone string) (cron.Schedule, error) {
	spec := expression
	if zone != "" {
		if _, err := time.LoadLocation(zone); err != nil {
			return nil, fmt.Errorf("unknown time_zone %s", zone)
		}
		spec = "CRON_TZ=" + zone + " " + spec
	}
	schedule, err := workflowCronParser.Parse(spec)
	if err != nil {
		return nil, fmt.Errorf("invalid cron %q: %v", expression, err)
	}
	return schedule, nil
}
