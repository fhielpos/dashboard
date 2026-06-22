package api

import (
	"embed"
	"fmt"
	"html/template"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/franco/dashboard/internal/core"
)

//go:embed templates
var templateFS embed.FS

var validPerPage = map[int]bool{5: true, 10: true, 15: true}
const defaultPerPage = 10

// ── Template data types ───────────────────────────────────────────────────────

type pageData struct {
	Name            string
	Today           time.Time
	CalEnabled      bool
	Cal             calendarData
	Tasks           []core.Item
	PRs             listData
	Issues          listData
	Notifs          listData
	Releases        []core.Item
	ReleasesEnabled bool
	Statuses        []core.Item
}

type calendarData struct {
	View      string
	Days      []calDay
	MonthName string
	Month     int
	Year      int
	PrevMonth int
	PrevYear  int
	NextMonth int
	NextYear  int
}

type calDay struct {
	Date    time.Time
	Events  []core.Item
	IsToday bool
	InMonth bool
}

type listData struct {
	Items          []core.Item
	RepoGroups     []repoGroup // populated when ActiveSort == "repo"
	Filters        []filterOpt
	ActiveFilter   string
	ActiveSort     string
	Page           int
	TotalPages     int
	Total          int
	PerPage        int
	Endpoint       string   // e.g. /htmx/prs
	TargetID       string   // e.g. prs-body
	AllAuthors     []string // all unique PR authors (for the exclusion panel)
	ExcludeAuthors []string // authors currently excluded via URL param
	ExclSuffix     string   // "&excl=..." fragment carried through pagination/filter links
}

type repoGroup struct {
	Repo  string
	Items []core.Item
}

type filterOpt struct {
	Value  string
	Label  string
	Count  int
	Active bool
}

// ── Handler ───────────────────────────────────────────────────────────────────

type uiHandler struct {
	registry *core.Registry
	name     string
	loc      *time.Location
	tmpl     *template.Template
}

func newUIHandler(registry *core.Registry, name, tz string) (*uiHandler, error) {
	loc := time.UTC
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	funcMap := template.FuncMap{
		"formatDate": func(t time.Time) string { return t.Format("Monday, January 2, 2006") },
		"formatTime": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			return t.In(loc).Format("15:04")
		},
		"dayAbbr": func(t time.Time) string { return t.Format("Mon") },
		"dayNum":  func(t time.Time) int { return t.Day() },
		"isOverdue": func(t *time.Time) bool {
			return t != nil && t.Before(time.Now())
		},
		"formatDue": func(t *time.Time) string {
			if t == nil {
				return ""
			}
			now := time.Now()
			today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
			d := *t
			switch {
			case sameDay(d, today):
				return "Today"
			case sameDay(d, today.AddDate(0, 0, -1)):
				return "Yesterday"
			case sameDay(d, today.AddDate(0, 0, 1)):
				return "Tomorrow"
			default:
				return d.Format("Jan 2")
			}
		},
		"ghRepo": func(meta map[string]interface{}) string {
			v, _ := meta["repository"].(string)
			return v
		},
		"ghNumber": func(meta map[string]interface{}) string {
			switch v := meta["number"].(type) {
			case int:
				return "#" + strconv.Itoa(v)
			case float64:
				return "#" + strconv.Itoa(int(v))
			}
			return ""
		},
		"ghUpdated": func(meta map[string]interface{}) string {
			t, ok := meta["updated_at"].(time.Time)
			if !ok {
				return ""
			}
			return timeAgo(t)
		},
		"categories": func(meta map[string]interface{}) []string {
			cats, _ := meta["categories"].([]string)
			return cats
		},
		"catLabel": func(cat string) string {
			return categoryLabel(cat)
		},
		"catBadgeClass": func(cat string) string {
			switch cat {
			case "review_requested":
				return "badge-yellow"
			case "approved_pending_merge":
				return "badge-green"
			case "mentioned":
				return "badge-purple"
			case "authored":
				return "badge-blue"
			case "assigned":
				return "badge-blue"
			default:
				return "badge-gray"
			}
		},
		"notifReason": func(meta map[string]interface{}) string {
			r, _ := meta["reason"].(string)
			labels := map[string]string{
				"mention":          "Mentioned",
				"review_requested": "Review Requested",
				"assign":           "Assigned",
				"comment":          "Comment",
				"state_change":     "State Change",
				"subscribed":       "Subscribed",
				"author":           "Author",
				"team_mention":     "Team Mention",
				"ci_activity":      "CI Activity",
			}
			if l, ok := labels[r]; ok {
				return l
			}
			return r
		},
		"notifReasonClass": func(meta map[string]interface{}) string {
			r, _ := meta["reason"].(string)
			switch r {
			case "mention", "team_mention":
				return "badge-purple"
			case "review_requested":
				return "badge-yellow"
			case "assign":
				return "badge-blue"
			default:
				return "badge-gray"
			}
		},
		"notifType": func(meta map[string]interface{}) string {
			t, _ := meta["subject_type"].(string)
			return t
		},
		"calColor": func(meta map[string]interface{}) string {
			v, _ := meta["cal_color"].(string)
			return v
		},
		"calName": func(meta map[string]interface{}) string {
			v, _ := meta["cal_name"].(string)
			return v
		},
		"statusClass": func(indicator string) string {
			switch indicator {
			case "none":
				return "status-ok"
			case "minor":
				return "status-minor"
			case "major", "critical":
				return "status-critical"
			default:
				return "status-unknown"
			}
		},
		"statusDesc": func(meta map[string]interface{}) string {
			v, _ := meta["description"].(string)
			return v
		},
		"relPublished": func(meta map[string]interface{}) string {
			t, ok := meta["published_at"].(time.Time)
			if !ok {
				return ""
			}
			return timeAgo(t)
		},
		"relTag": func(meta map[string]interface{}) string {
			v, _ := meta["tag_name"].(string)
			return v
		},
		"relPrerelease": func(meta map[string]interface{}) bool {
			v, _ := meta["prerelease"].(bool)
			return v
		},
		"add": func(a, b int) int { return a + b },
		"sub": func(a, b int) int { return a - b },
		"seq": func(n int) []int {
			s := make([]int, n)
			for i := range s {
				s[i] = i + 1
			}
			return s
		},
		"pageURL": func(endpoint, filter, sortBy string, page, pp int) string {
			return fmt.Sprintf("%s?page=%d&filter=%s&sort=%s&pp=%d", endpoint, page, filter, sortBy, pp)
		},
		"contains": func(slice []string, item string) bool {
			for _, s := range slice {
				if strings.EqualFold(s, item) {
					return true
				}
			}
			return false
		},
		"exclToggleURL": func(endpoint, filter, sortBy string, page, pp int, excls []string, author string) string {
			next := make([]string, 0, len(excls))
			found := false
			for _, a := range excls {
				if strings.EqualFold(a, author) {
					found = true
				} else {
					next = append(next, a)
				}
			}
			if !found {
				next = append(next, author)
			}
			u := fmt.Sprintf("%s?page=1&filter=%s&sort=%s&pp=%d", endpoint, filter, sortBy, pp)
			for _, a := range next {
				u += "&excl=" + url.QueryEscape(a)
			}
			return u
		},
	}

	tmpl, err := template.New("").Funcs(funcMap).ParseFS(templateFS, "templates/dashboard.html")
	if err != nil {
		return nil, fmt.Errorf("parsing templates: %w", err)
	}
	return &uiHandler{registry: registry, name: name, loc: loc, tmpl: tmpl}, nil
}

func (h *uiHandler) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := h.tmpl.ExecuteTemplate(w, name, data); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}

// ── Full page ─────────────────────────────────────────────────────────────────

func (h *uiHandler) handleIndex(w http.ResponseWriter, r *http.Request) {
	all := h.registry.Items("", "")
	_, calEnabled := h.registry.Plugin("calendar")
	_, relEnabled := h.registry.Plugin("releases")
	now := time.Now().In(h.loc)
	data := pageData{
		Name:            h.name,
		Today:           now,
		CalEnabled:      calEnabled,
		Cal:             buildCalendarData("daily", all, now.Year(), int(now.Month()), h.loc),
		Tasks:           todayTasks(all, h.loc),
		PRs:             buildListData(itemsByType(all, core.ItemTypePR), "all", "activity", 1, defaultPerPage, "/htmx/prs", "prs-body", prFilters),
		Issues:          buildListData(itemsByType(all, core.ItemTypeIssue), "all", "activity", 1, defaultPerPage, "/htmx/issues", "issues-body", issueFilters),
		Notifs:          buildNotifData(itemsByType(all, "notification"), "all", 1, defaultPerPage),
		Releases:        itemsByType(all, core.ItemTypeRelease),
		ReleasesEnabled: relEnabled,
		Statuses:        itemsByType(all, core.ItemTypeStatus),
	}
	h.render(w, "layout", data)
}

// ── Calendar ──────────────────────────────────────────────────────────────────

func (h *uiHandler) handleCalendar(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	view := q.Get("view")
	if view == "" {
		view = "daily"
	}
	now := time.Now().In(h.loc)
	month := queryInt(q.Get("month"), int(now.Month()))
	year := queryInt(q.Get("year"), now.Year())
	h.render(w, "calendar", buildCalendarData(view, h.registry.Items("", ""), year, month, h.loc))
}

// ── Tasks ─────────────────────────────────────────────────────────────────────

func (h *uiHandler) handleTasks(w http.ResponseWriter, r *http.Request) {
	h.render(w, "tasks", todayTasks(h.registry.Items("", ""), h.loc))
}

func (h *uiHandler) handleCreateTask(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad request", http.StatusBadRequest)
		return
	}
	title := r.FormValue("title")
	if title == "" {
		h.handleTasks(w, r)
		return
	}
	p, ok := h.registry.Plugin("tasks")
	if !ok {
		http.Error(w, "tasks plugin not registered", http.StatusNotFound)
		return
	}
	wp := p.(core.WritablePlugin)
	item := core.Item{
		Title:    title,
		Metadata: map[string]interface{}{},
	}
	if r.FormValue("important") == "1" {
		item.Metadata["important"] = true
	}
	if _, err := wp.CreateItem(r.Context(), item); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.registry.Refresh(r.Context(), "tasks")
	h.handleTasks(w, r)
}

func (h *uiHandler) handleToggleImportant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.registry.Plugin("tasks")
	if !ok {
		http.Error(w, "tasks plugin not registered", http.StatusNotFound)
		return
	}
	wp := p.(core.WritablePlugin)

	// Read current state to toggle.
	all := h.registry.Items("tasks", "")
	var current bool
	for _, item := range all {
		if item.ID == id {
			current, _ = item.Metadata["important"].(bool)
			break
		}
	}
	updates := map[string]interface{}{"important": !current}
	if _, err := wp.UpdateItem(r.Context(), id, updates); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.registry.Refresh(r.Context(), "tasks")
	h.handleTasks(w, r)
}

func (h *uiHandler) handleDeleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.registry.Plugin("tasks")
	if !ok {
		http.Error(w, "tasks plugin not registered", http.StatusNotFound)
		return
	}
	wp := p.(core.WritablePlugin)
	if err := wp.DeleteItem(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.registry.Refresh(r.Context(), "tasks")
	h.handleTasks(w, r)
}

func (h *uiHandler) handleCompleteTask(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.registry.Plugin("tasks")
	if !ok {
		http.Error(w, "tasks plugin not registered", http.StatusNotFound)
		return
	}
	wp := p.(core.WritablePlugin)

	// Toggle: done → open, anything else → done.
	newStatus := "done"
	for _, item := range h.registry.Items("tasks", "") {
		if item.ID == id && (item.Status == "done" || item.Status == "completed") {
			newStatus = "open"
			break
		}
	}
	if _, err := wp.UpdateItem(r.Context(), id, map[string]interface{}{"status": newStatus}); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.registry.Refresh(r.Context(), "tasks")
	h.handleTasks(w, r)
}

// ── PRs ───────────────────────────────────────────────────────────────────────

func (h *uiHandler) handlePRs(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := q.Get("filter")
	sortBy := q.Get("sort")
	page := queryInt(q.Get("page"), 1)
	pp := queryInt(q.Get("pp"), defaultPerPage)
	excluded := q["excl"] // multi-value: one per excluded author

	allPRs := itemsByType(h.registry.Items("", ""), core.ItemTypePR)

	// Collect all unique authors for the exclusion panel (before filtering).
	authorSet := make(map[string]struct{})
	for _, item := range allPRs {
		if a, _ := item.Metadata["author"].(string); a != "" {
			authorSet[a] = struct{}{}
		}
	}
	allAuthors := make([]string, 0, len(authorSet))
	for a := range authorSet {
		allAuthors = append(allAuthors, a)
	}
	sort.Strings(allAuthors)

	// Apply author exclusions.
	prs := allPRs
	if len(excluded) > 0 {
		excSet := make(map[string]struct{}, len(excluded))
		for _, a := range excluded {
			excSet[strings.ToLower(a)] = struct{}{}
		}
		kept := make([]core.Item, 0, len(allPRs))
		for _, item := range allPRs {
			author, _ := item.Metadata["author"].(string)
			if _, ex := excSet[strings.ToLower(author)]; !ex {
				kept = append(kept, item)
			}
		}
		prs = kept
	}

	// Build suffix to carry exclusions through pagination/filter links.
	var sb strings.Builder
	for _, a := range excluded {
		sb.WriteString("&excl=")
		sb.WriteString(url.QueryEscape(a))
	}

	data := buildListData(prs, filter, sortBy, page, pp, "/htmx/prs", "prs-body", prFilters)
	data.AllAuthors = allAuthors
	data.ExcludeAuthors = excluded
	data.ExclSuffix = sb.String()
	h.render(w, "prlist", data)
}

// ── Issues ────────────────────────────────────────────────────────────────────

func (h *uiHandler) handleIssues(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := q.Get("filter")
	sortBy := q.Get("sort")
	page := queryInt(q.Get("page"), 1)
	pp := queryInt(q.Get("pp"), defaultPerPage)
	all := itemsByType(h.registry.Items("", ""), core.ItemTypeIssue)
	h.render(w, "issuelist", buildListData(all, filter, sortBy, page, pp, "/htmx/issues", "issues-body", issueFilters))
}

// ── Notifications ─────────────────────────────────────────────────────────────

func (h *uiHandler) handleNotifications(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	filter := q.Get("filter")
	page := queryInt(q.Get("page"), 1)
	pp := queryInt(q.Get("pp"), defaultPerPage)
	all := itemsByType(h.registry.Items("", ""), "notification")
	h.render(w, "notifications", buildNotifData(all, filter, page, pp))
}

func buildNotifData(all []core.Item, filter string, page, pp int) listData {
	if !validPerPage[pp] {
		pp = defaultPerPage
	}
	if filter == "" {
		filter = "all"
	}
	filters := make([]filterOpt, 0, len(notifFilterDefs))
	for _, f := range notifFilterDefs {
		count := len(filterByReason(all, f))
		if f == "all" {
			count = len(all)
		}
		label := categoryLabel(f)
		if label == f {
			label = notifReasonLabel(f)
		}
		filters = append(filters, filterOpt{Value: f, Label: label, Count: count, Active: f == filter})
	}

	filtered := all
	if filter != "all" {
		filtered = filterByReason(all, filter)
	}

	total := len(filtered)
	totalPages := int(math.Ceil(float64(total) / float64(pp)))
	if totalPages == 0 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}
	start := (page - 1) * pp
	end := start + pp
	if end > total {
		end = total
	}
	var pageItems []core.Item
	if start < total {
		pageItems = filtered[start:end]
	}
	return listData{
		Items: pageItems, Filters: filters,
		ActiveFilter: filter, ActiveSort: "",
		Page: page, TotalPages: totalPages, Total: total, PerPage: pp,
		Endpoint: "/htmx/notifications", TargetID: "notifs-body",
	}
}

func filterByReason(items []core.Item, reason string) []core.Item {
	var result []core.Item
	for _, item := range items {
		if r, _ := item.Metadata["reason"].(string); r == reason {
			result = append(result, item)
		}
	}
	return result
}

func notifReasonLabel(reason string) string {
	labels := map[string]string{
		"all":          "All",
		"mention":      "Mentions",
		"assign":       "Assigned",
		"comment":      "Comments",
		"state_change": "State Change",
	}
	if l, ok := labels[reason]; ok {
		return l
	}
	return reason
}

func (h *uiHandler) handleDismissNotification(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	p, ok := h.registry.Plugin("notifications")
	if !ok {
		http.Error(w, "notifications plugin not registered", http.StatusNotFound)
		return
	}
	wp, ok := p.(core.WritablePlugin)
	if !ok {
		http.Error(w, "notifications plugin not writable", http.StatusInternalServerError)
		return
	}
	if err := wp.DeleteItem(r.Context(), id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = h.registry.Refresh(r.Context(), "notifications")
	h.handleNotifications(w, r)
}

// ── Releases ──────────────────────────────────────────────────────────────────

func (h *uiHandler) handleReleases(w http.ResponseWriter, r *http.Request) {
	h.render(w, "releases", itemsByType(h.registry.Items("", ""), core.ItemTypeRelease))
}

// ── List data builder ─────────────────────────────────────────────────────────

var prFilters = []string{"all", "review_requested", "approved_pending_merge", "mentioned", "authored"}
var issueFilters = []string{"all", "assigned", "mentioned", "authored"}
var notifFilterDefs = []string{"all", "mention", "assign", "comment", "state_change"}

func buildListData(all []core.Item, filter, sortBy string, page, pp int, endpoint, targetID string, filterDefs []string) listData {
	if !validPerPage[pp] {
		pp = defaultPerPage
	}
	if filter == "" {
		filter = "all"
	}
	if sortBy == "" {
		sortBy = "activity"
	}

	// Build filter options with counts.
	filters := make([]filterOpt, 0, len(filterDefs))
	for _, f := range filterDefs {
		var filtered []core.Item
		if f == "all" {
			filtered = all
		} else {
			filtered = filterByCategory(all, f)
		}
		filters = append(filters, filterOpt{
			Value:  f,
			Label:  categoryLabel(f),
			Count:  len(filtered),
			Active: f == filter,
		})
	}

	// Apply filter.
	var filtered []core.Item
	if filter == "all" {
		filtered = all
	} else {
		filtered = filterByCategory(all, filter)
	}

	// Apply sort.
	filtered = sortItems(filtered, sortBy)

	// Paginate.
	total := len(filtered)
	totalPages := int(math.Ceil(float64(total) / float64(pp)))
	if totalPages == 0 {
		totalPages = 1
	}
	if page < 1 {
		page = 1
	}
	if page > totalPages {
		page = totalPages
	}

	start := (page - 1) * pp
	end := start + pp
	if end > total {
		end = total
	}
	var pageItems []core.Item
	if start < total {
		pageItems = filtered[start:end]
	}

	data := listData{
		Items:        pageItems,
		Filters:      filters,
		ActiveFilter: filter,
		ActiveSort:   sortBy,
		Page:         page,
		TotalPages:   totalPages,
		Total:        total,
		PerPage:      pp,
		Endpoint:     endpoint,
		TargetID:     targetID,
	}

	if sortBy == "repo" {
		var groups []repoGroup
		for _, item := range pageItems {
			repo, _ := item.Metadata["repository"].(string)
			if len(groups) == 0 || groups[len(groups)-1].Repo != repo {
				groups = append(groups, repoGroup{Repo: repo})
			}
			groups[len(groups)-1].Items = append(groups[len(groups)-1].Items, item)
		}
		data.RepoGroups = groups
	}

	return data
}

// ── Calendar helpers ──────────────────────────────────────────────────────────

func buildCalendarData(view string, items []core.Item, year, month int, loc *time.Location) calendarData {
	now := time.Now().In(loc)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)
	events := itemsByType(items, core.ItemTypeEvent)

	// Navigation helpers.
	cur := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
	prev := cur.AddDate(0, -1, 0)
	next := cur.AddDate(0, 1, 0)
	nav := func(t time.Time) (int, int) { return t.Year(), int(t.Month()) }
	py, pm := nav(prev)
	ny, nm := nav(next)

	base := calendarData{
		View:      view,
		MonthName: cur.Format("January"),
		Month:     month,
		Year:      year,
		PrevYear:  py, PrevMonth: pm,
		NextYear:  ny, NextMonth: nm,
	}

	switch view {
	case "weekly":
		wd := int(today.Weekday())
		if wd == 0 {
			wd = 7
		}
		monday := today.AddDate(0, 0, -(wd - 1))
		days := make([]calDay, 7)
		for i := range days {
			d := monday.AddDate(0, 0, i)
			days[i] = calDay{Date: d, Events: eventsForDay(events, d, loc), IsToday: d.Equal(today), InMonth: true}
		}
		base.Days = days
		return base

	case "monthly":
		first := time.Date(year, time.Month(month), 1, 0, 0, 0, 0, loc)
		wdFirst := int(first.Weekday())
		if wdFirst == 0 {
			wdFirst = 7
		}
		gridStart := first.AddDate(0, 0, -(wdFirst - 1))

		// Last day of the month.
		last := time.Date(year, time.Month(month+1), 0, 0, 0, 0, 0, loc)
		wdLast := int(last.Weekday())
		if wdLast == 0 {
			wdLast = 7
		}
		// Complete the final week, but if month ends on Monday (start of a new
		// week) don't add 6 trailing days from next month — end the grid there.
		var trailing int
		if wdLast != 1 {
			trailing = (7 - wdLast) % 7
		}
		gridEnd := last.AddDate(0, 0, trailing)
		total := int(gridEnd.Sub(gridStart).Hours()/24) + 1

		days := make([]calDay, total)
		for i := range days {
			d := gridStart.AddDate(0, 0, i)
			days[i] = calDay{Date: d, Events: eventsForDay(events, d, loc), IsToday: d.Equal(today), InMonth: d.Month() == time.Month(month)}
		}
		base.Days = days
		return base

	default: // daily
		base.Days = []calDay{{Date: today, Events: eventsForDay(events, today, loc), IsToday: true, InMonth: true}}
		return base
	}
}

func eventsForDay(events []core.Item, day time.Time, loc *time.Location) []core.Item {
	var result []core.Item
	for _, e := range events {
		if e.StartAt == nil {
			continue
		}
		// Convert to the configured timezone before comparing the calendar day.
		d := e.StartAt.In(loc)
		if d.Year() == day.Year() && d.Month() == day.Month() && d.Day() == day.Day() {
			result = append(result, e)
		}
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].StartAt == nil || result[j].StartAt == nil {
			return false
		}
		return result[i].StartAt.Before(*result[j].StartAt)
	})
	return result
}

// ── Task helpers ──────────────────────────────────────────────────────────────

func todayTasks(items []core.Item, loc *time.Location) []core.Item {
	now := time.Now().In(loc)
	endOfDay := time.Date(now.Year(), now.Month(), now.Day(), 23, 59, 59, 0, loc)

	startOfDay := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, loc)

	var open, done []core.Item
	for _, item := range items {
		if item.Type != core.ItemTypeTask {
			continue
		}
		isDone := item.Status == "done" || item.Status == "completed"
		if isDone {
			completedStr, _ := item.Metadata["completed_at"].(string)
			t, err := time.Parse(time.RFC3339, completedStr)
			if err != nil || t.In(loc).Before(startOfDay) {
				continue // completed before today — skip
			}
			done = append(done, item)
			continue
		}
		if item.DueAt == nil || !item.DueAt.After(endOfDay) {
			open = append(open, item)
		}
	}

	sort.Slice(open, func(i, j int) bool {
		// Important tasks first.
		ii, _ := open[i].Metadata["important"].(bool)
		ij, _ := open[j].Metadata["important"].(bool)
		if ii != ij {
			return ii
		}
		if open[i].Priority != open[j].Priority {
			return open[i].Priority > open[j].Priority
		}
		if open[i].DueAt == nil {
			return false
		}
		if open[j].DueAt == nil {
			return true
		}
		return open[i].DueAt.Before(*open[j].DueAt)
	})

	// Completed tasks go at the end, most recently completed first.
	sort.Slice(done, func(i, j int) bool {
		return done[i].FetchedAt.After(done[j].FetchedAt)
	})

	return append(open, done...)
}

// ── Item helpers ──────────────────────────────────────────────────────────────

func itemsByType(items []core.Item, t core.ItemType) []core.Item {
	var result []core.Item
	for _, item := range items {
		if item.Type == t {
			result = append(result, item)
		}
	}
	return result
}

func filterByCategory(items []core.Item, cat string) []core.Item {
	var result []core.Item
	for _, item := range items {
		if cats, ok := item.Metadata["categories"].([]string); ok {
			for _, c := range cats {
				if c == cat {
					result = append(result, item)
					break
				}
			}
		}
	}
	return result
}

func sortItems(items []core.Item, by string) []core.Item {
	sorted := make([]core.Item, len(items))
	copy(sorted, items)
	switch by {
	case "age":
		sort.SliceStable(sorted, func(i, j int) bool {
			return metaTime(sorted[i].Metadata, "updated_at").Before(metaTime(sorted[j].Metadata, "updated_at"))
		})
	case "repo":
		sort.SliceStable(sorted, func(i, j int) bool {
			ri, _ := sorted[i].Metadata["repository"].(string)
			rj, _ := sorted[j].Metadata["repository"].(string)
			if ri != rj {
				return ri < rj
			}
			// Within the same repo, sort by activity (newest first).
			return metaTime(sorted[i].Metadata, "updated_at").After(metaTime(sorted[j].Metadata, "updated_at"))
		})
	default: // "activity"
		sort.SliceStable(sorted, func(i, j int) bool {
			return metaTime(sorted[i].Metadata, "updated_at").After(metaTime(sorted[j].Metadata, "updated_at"))
		})
	}
	return sorted
}

func metaTime(meta map[string]interface{}, key string) time.Time {
	if v, ok := meta[key].(time.Time); ok {
		return v
	}
	return time.Time{}
}

// ── Label helpers ─────────────────────────────────────────────────────────────

func categoryLabel(cat string) string {
	labels := map[string]string{
		"all":                   "All",
		"authored":              "Created by me",
		"review_requested":      "Review Requested",
		"mentioned":             "Mentioned",
		"approved_pending_merge": "Approved",
		"assigned":              "Assigned",
	}
	if l, ok := labels[cat]; ok {
		return l
	}
	return cat
}

func timeAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	case d < 7*24*time.Hour:
		return fmt.Sprintf("%dd ago", int(d.Hours()/24))
	default:
		return t.Format("Jan 2")
	}
}

// ── Small utilities ───────────────────────────────────────────────────────────

func sameDay(a, b time.Time) bool {
	return a.Year() == b.Year() && a.Month() == b.Month() && a.Day() == b.Day()
}

func queryInt(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil && v > 0 {
		return v
	}
	return def
}
