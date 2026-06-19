package calendar

import (
	"context"
	"fmt"
	"net/http"
	"time"

	ical "github.com/emersion/go-ical"

	"github.com/franco/dashboard/internal/core"
)

type calendarSource struct {
	name  string
	url   string
	color string // CSS color, e.g. "#4285f4"
}

// Plugin fetches events from one or more iCal URLs.
type Plugin struct {
	calendars []calendarSource
	client    *http.Client
	lookahead time.Duration
	// loc is used to interpret floating times (those without an explicit TZID).
	loc *time.Location
}

func New() *Plugin {
	return &Plugin{}
}

func (p *Plugin) Name() string        { return "calendar" }
func (p *Plugin) Description() string { return "Calendar events from iCal URLs" }

func (p *Plugin) Initialize(_ context.Context, config map[string]interface{}) error {
	// New format: calendars list with name, url, color.
	if raw, ok := config["calendars"]; ok {
		list, ok := raw.([]interface{})
		if !ok {
			return fmt.Errorf("calendar: calendars must be a list")
		}
		for _, entry := range list {
			m, ok := entry.(map[string]interface{})
			if !ok {
				return fmt.Errorf("calendar: each calendar entry must be a map")
			}
			url, _ := m["url"].(string)
			if url == "" {
				return fmt.Errorf("calendar: each calendar entry requires a url")
			}
			name, _ := m["name"].(string)
			color, _ := m["color"].(string)
			p.calendars = append(p.calendars, calendarSource{name: name, url: url, color: color})
		}
	} else if raw, ok := config["urls"]; ok {
		// Legacy format: plain urls list.
		switch v := raw.(type) {
		case []interface{}:
			for _, u := range v {
				s, ok := u.(string)
				if !ok {
					return fmt.Errorf("calendar: urls must be strings")
				}
				p.calendars = append(p.calendars, calendarSource{url: s})
			}
		case string:
			p.calendars = []calendarSource{{url: v}}
		default:
			return fmt.Errorf("calendar: urls must be a list of strings")
		}
	} else {
		return fmt.Errorf("calendar: calendars (or urls) is required")
	}

	// Default lookahead: 30 days
	p.lookahead = 30 * 24 * time.Hour
	if la, ok := config["lookahead_days"].(int); ok && la > 0 {
		p.lookahead = time.Duration(la) * 24 * time.Hour
	}

	p.client = &http.Client{Timeout: 15 * time.Second}

	p.loc = time.UTC
	if tz, ok := config["timezone"].(string); ok && tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			p.loc = l
		}
	}
	return nil
}

func (p *Plugin) Fetch(ctx context.Context) ([]core.Item, error) {
	var items []core.Item
	for _, cal := range p.calendars {
		fetched, err := p.fetchURL(ctx, cal)
		if err != nil {
			return nil, fmt.Errorf("calendar: fetching %s: %w", cal.url, err)
		}
		items = append(items, fetched...)
	}
	return items, nil
}

func (p *Plugin) Shutdown(_ context.Context) error { return nil }

func (p *Plugin) fetchURL(ctx context.Context, src calendarSource) ([]core.Item, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	dec := ical.NewDecoder(resp.Body)
	cal, err := dec.Decode()
	if err != nil {
		return nil, fmt.Errorf("parsing iCal: %w", err)
	}

	now := time.Now()
	cutoff := now.Add(p.lookahead)
	var items []core.Item

	for _, comp := range cal.Children {
		if comp.Name != ical.CompEvent {
			continue
		}

		summary := propString(comp, ical.PropSummary)
		description := propString(comp, ical.PropDescription)
		uid := propString(comp, ical.PropUID)
		location := propString(comp, ical.PropLocation)

		start, err := propTime(comp, ical.PropDateTimeStart, p.loc)
		if err != nil || start.IsZero() {
			continue
		}

		// Skip events in the past or too far in the future.
		if start.Before(now.Add(-24*time.Hour)) || start.After(cutoff) {
			continue
		}

		end, _ := propTime(comp, ical.PropDateTimeEnd, p.loc)

		meta := map[string]interface{}{}
		if location != "" {
			meta["location"] = location
		}
		if src.color != "" {
			meta["cal_color"] = src.color
		}
		if src.name != "" {
			meta["cal_name"] = src.name
		}
		item := core.Item{
			ID:          fmt.Sprintf("calendar:%s", uid),
			Plugin:      p.Name(),
			Type:        core.ItemTypeEvent,
			Title:       summary,
			Description: description,
			StartAt:     &start,
			FetchedAt:   now,
			Metadata:    meta,
		}
		if !end.IsZero() {
			item.DueAt = &end
		}

		items = append(items, item)
	}

	return items, nil
}

func propString(comp *ical.Component, name string) string {
	if p := comp.Props.Get(name); p != nil {
		return p.Value
	}
	return ""
}

func propTime(comp *ical.Component, name string, loc *time.Location) (time.Time, error) {
	p := comp.Props.Get(name)
	if p == nil {
		return time.Time{}, fmt.Errorf("property %s not found", name)
	}
	return p.DateTime(loc)
}
