package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Songxwn/netbox-glean/internal/model"
	"github.com/Songxwn/netbox-glean/internal/netbox"
)

func writeJSON(w http.ResponseWriter, status int, value any) {
	data, err := json.Marshal(value)
	if err != nil {
		http.Error(w, "encode response failed", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(out); err != nil {
		writeError(w, http.StatusBadRequest, "请求 JSON 无效: "+err.Error())
		return false
	}
	return true
}

func readOptionalJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if r.ContentLength == 0 {
		return true
	}
	defer r.Body.Close()
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(out); err != nil {
		if errors.Is(err, io.EOF) {
			return true
		}
		writeError(w, http.StatusBadRequest, "请求 JSON 无效: "+err.Error())
		return false
	}
	return true
}

func (s *Server) settingsView() model.Settings {
	view := s.store.Settings()
	view.NetboxTokenSet = view.NetboxToken != ""
	view.NetboxToken = ""
	return view
}

func (s *Server) handleGetSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, s.settingsView())
}

func (s *Server) handlePutSettings(w http.ResponseWriter, r *http.Request) {
	var in model.Settings
	if !readJSON(w, r, &in) {
		return
	}
	current := s.store.Settings()
	if strings.TrimSpace(in.NetboxToken) == "" {
		in.NetboxToken = current.NetboxToken
	}
	in.NetboxURL = strings.TrimSpace(in.NetboxURL)
	in.TokenVersion = strings.ToLower(strings.TrimSpace(in.TokenVersion))
	if in.TokenVersion == "" {
		in.TokenVersion = "auto"
	}
	if in.TokenVersion != "auto" && in.TokenVersion != "1" && in.TokenVersion != "2" {
		writeError(w, http.StatusBadRequest, "token_version 必须是 auto、1 或 2")
		return
	}
	if in.PrimaryInterface == "" {
		in.PrimaryInterface = "mgmt0"
	}
	if in.UHeight == 0 {
		in.UHeight = 1
	}
	if in.DeviceRole == "" {
		in.DeviceRole = "server"
	}
	in.Site = strings.TrimSpace(in.Site)
	in.SiteName = strings.TrimSpace(in.SiteName)
	in.Platform = strings.TrimSpace(in.Platform)
	in.NetboxTokenSet = false
	if in.NetboxURL != "" {
		if _, err := netbox.New(netbox.Config{
			URL:          in.NetboxURL,
			Token:        in.NetboxToken,
			TokenVersion: in.TokenVersion,
		}); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
	}
	if err := s.store.UpdateSettings(in); err != nil {
		s.logger.Error("save settings", "error", err)
		writeError(w, http.StatusInternalServerError, "保存设置失败")
		return
	}
	writeJSON(w, http.StatusOK, s.settingsView())
}

type netboxTestRequest struct {
	NetboxURL      string `json:"netbox_url"`
	NetboxToken    string `json:"netbox_token"`
	TokenVersion   string `json:"token_version"`
	NetboxInsecure *bool  `json:"netbox_insecure"`
}

func (s *Server) handleNetboxTest(w http.ResponseWriter, r *http.Request) {
	var req netboxTestRequest
	if !readOptionalJSON(w, r, &req) {
		return
	}
	settings := s.store.Settings()
	if strings.TrimSpace(req.NetboxURL) != "" {
		settings.NetboxURL = strings.TrimSpace(req.NetboxURL)
	}
	if strings.TrimSpace(req.NetboxToken) != "" {
		settings.NetboxToken = strings.TrimSpace(req.NetboxToken)
	}
	if strings.TrimSpace(req.TokenVersion) != "" {
		settings.TokenVersion = strings.TrimSpace(req.TokenVersion)
	}
	if req.NetboxInsecure != nil {
		settings.NetboxInsecure = *req.NetboxInsecure
	}
	if settings.NetboxURL == "" {
		writeError(w, http.StatusBadRequest, "请先填写 NetBox URL")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	result, err := netbox.Test(ctx, settings)
	if err != nil {
		writeError(w, http.StatusBadGateway, "连接 NetBox 失败: "+err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"netbox_version":  result.NetboxVersion,
		"auth_mode":       result.AuthMode,
		"base_url":        result.BaseURL,
	})
}

func (s *Server) handleStats(w http.ResponseWriter, _ *http.Request) {
	targets := s.store.Targets()
	byProtocol := map[string]int{}
	enabled := 0
	for _, t := range targets {
		byProtocol[t.Protocol]++
		if t.Enabled {
			enabled++
		}
	}
	ipTargets := s.store.IPTargets()
	ipEnabled := 0
	for _, t := range ipTargets {
		if t.Enabled {
			ipEnabled++
		}
	}
	settings := s.store.Settings()
	writeJSON(w, http.StatusOK, map[string]any{
		"targets":            len(targets),
		"targets_enabled":    enabled,
		"ip_targets":         len(ipTargets),
		"ip_targets_enabled": ipEnabled,
		"by_protocol":        byProtocol,
		"jobs":               s.jobs.Stats(),
		"netbox_configured":  settings.NetboxURL != "",
		"site":               settings.Site,
	})
}

func (s *Server) handleListTargets(w http.ResponseWriter, _ *http.Request) {
	targets := s.store.Targets()
	out := make([]model.Target, 0, len(targets))
	for _, t := range targets {
		out = append(out, sanitizeTarget(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetTarget(w http.ResponseWriter, r *http.Request) {
	target, ok := s.store.Target(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "目标不存在")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeTarget(target))
}

func (s *Server) handleCreateTarget(w http.ResponseWriter, r *http.Request) {
	var target model.Target
	if !readJSON(w, r, &target) {
		return
	}
	target.ID = ""
	target.CreatedAt = time.Time{}
	target.UpdatedAt = time.Time{}
	target.LastRunAt = nil
	model.NormalizeTarget(&target)
	if err := model.ValidateTarget(target); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SaveTarget(target)
	if err != nil {
		s.logger.Error("save target", "error", err)
		writeError(w, http.StatusInternalServerError, "保存目标失败")
		return
	}
	writeJSON(w, http.StatusCreated, sanitizeTarget(saved))
}

func (s *Server) handleUpdateTarget(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.store.Target(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "目标不存在")
		return
	}
	var target model.Target
	if !readJSON(w, r, &target) {
		return
	}
	target.ID = existing.ID
	target.CreatedAt = existing.CreatedAt
	target.LastRunAt = existing.LastRunAt
	mergeTargetSecrets(&target, existing)
	model.NormalizeTarget(&target)
	if err := model.ValidateTarget(target); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SaveTarget(target)
	if err != nil {
		s.logger.Error("save target", "error", err)
		writeError(w, http.StatusInternalServerError, "保存目标失败")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeTarget(saved))
}

func (s *Server) handleDeleteTarget(w http.ResponseWriter, r *http.Request) {
	found, err := s.store.DeleteTarget(r.PathValue("id"))
	if err != nil {
		s.logger.Error("delete target", "error", err)
		writeError(w, http.StatusInternalServerError, "删除目标失败")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "目标不存在")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCollectTarget(w http.ResponseWriter, r *http.Request) {
	target, ok := s.store.Target(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "目标不存在")
		return
	}
	var req struct {
		Sync bool `json:"sync"`
	}
	if !readOptionalJSON(w, r, &req) {
		return
	}
	jobID := s.startJob(target, req.Sync)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":      jobID,
		"target_id":   target.ID,
		"target_name": target.Name,
		"sync":        req.Sync,
	})
}

func (s *Server) handleCollectBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetIDs []string `json:"target_ids"`
		Sync      bool     `json:"sync"`
	}
	if !readOptionalJSON(w, r, &req) {
		return
	}
	var targets []model.Target
	if len(req.TargetIDs) > 0 {
		for _, id := range req.TargetIDs {
			if target, ok := s.store.Target(id); ok {
				targets = append(targets, target)
			}
		}
	} else {
		for _, target := range s.store.Targets() {
			if target.Enabled {
				targets = append(targets, target)
			}
		}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, "没有可执行的目标")
		return
	}
	type started struct {
		JobID      string `json:"job_id"`
		TargetID   string `json:"target_id"`
		TargetName string `json:"target_name"`
	}
	jobs := make([]started, 0, len(targets))
	for _, target := range targets {
		jobs = append(jobs, started{
			JobID:      s.startJob(target, req.Sync),
			TargetID:   target.ID,
			TargetName: target.Name,
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"count": len(jobs), "jobs": jobs})
}

func (s *Server) handleListIPTargets(w http.ResponseWriter, _ *http.Request) {
	targets := s.store.IPTargets()
	out := make([]model.IPTarget, 0, len(targets))
	for _, t := range targets {
		out = append(out, sanitizeIPTarget(t))
	}
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) handleGetIPTarget(w http.ResponseWriter, r *http.Request) {
	target, ok := s.store.IPTarget(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "IP 采集目标不存在")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeIPTarget(target))
}

func (s *Server) handleCreateIPTarget(w http.ResponseWriter, r *http.Request) {
	var target model.IPTarget
	if !readJSON(w, r, &target) {
		return
	}
	target.ID = ""
	target.CreatedAt = time.Time{}
	target.UpdatedAt = time.Time{}
	target.LastRunAt = nil
	model.NormalizeIPTarget(&target)
	if err := model.ValidateIPTarget(target); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SaveIPTarget(target)
	if err != nil {
		s.logger.Error("save ip target", "error", err)
		writeError(w, http.StatusInternalServerError, "保存 IP 采集目标失败")
		return
	}
	writeJSON(w, http.StatusCreated, sanitizeIPTarget(saved))
}

func (s *Server) handleUpdateIPTarget(w http.ResponseWriter, r *http.Request) {
	existing, ok := s.store.IPTarget(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "IP 采集目标不存在")
		return
	}
	var target model.IPTarget
	if !readJSON(w, r, &target) {
		return
	}
	target.ID = existing.ID
	target.CreatedAt = existing.CreatedAt
	target.LastRunAt = existing.LastRunAt
	mergeIPTargetSecrets(&target, existing)
	model.NormalizeIPTarget(&target)
	if err := model.ValidateIPTarget(target); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	saved, err := s.store.SaveIPTarget(target)
	if err != nil {
		s.logger.Error("save ip target", "error", err)
		writeError(w, http.StatusInternalServerError, "保存 IP 采集目标失败")
		return
	}
	writeJSON(w, http.StatusOK, sanitizeIPTarget(saved))
}

func (s *Server) handleDeleteIPTarget(w http.ResponseWriter, r *http.Request) {
	found, err := s.store.DeleteIPTarget(r.PathValue("id"))
	if err != nil {
		s.logger.Error("delete ip target", "error", err)
		writeError(w, http.StatusInternalServerError, "删除 IP 采集目标失败")
		return
	}
	if !found {
		writeError(w, http.StatusNotFound, "IP 采集目标不存在")
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleCollectIPTarget(w http.ResponseWriter, r *http.Request) {
	target, ok := s.store.IPTarget(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "IP 采集目标不存在")
		return
	}
	var req struct {
		Sync bool `json:"sync"`
	}
	if !readOptionalJSON(w, r, &req) {
		return
	}
	jobID := s.startIPJob(target, req.Sync)
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":      jobID,
		"target_id":   target.ID,
		"target_name": target.Name,
		"sync":        req.Sync,
	})
}

func (s *Server) handleCollectIPBatch(w http.ResponseWriter, r *http.Request) {
	var req struct {
		TargetIDs []string `json:"target_ids"`
		Sync      bool     `json:"sync"`
	}
	if !readOptionalJSON(w, r, &req) {
		return
	}
	var targets []model.IPTarget
	if len(req.TargetIDs) > 0 {
		for _, id := range req.TargetIDs {
			if target, ok := s.store.IPTarget(id); ok {
				targets = append(targets, target)
			}
		}
	} else {
		for _, target := range s.store.IPTargets() {
			if target.Enabled {
				targets = append(targets, target)
			}
		}
	}
	if len(targets) == 0 {
		writeError(w, http.StatusBadRequest, "没有可执行的 IP 采集目标")
		return
	}
	type started struct {
		JobID      string `json:"job_id"`
		TargetID   string `json:"target_id"`
		TargetName string `json:"target_name"`
	}
	out := make([]started, 0, len(targets))
	for _, target := range targets {
		out = append(out, started{
			JobID:      s.startIPJob(target, req.Sync),
			TargetID:   target.ID,
			TargetName: target.Name,
		})
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"count": len(out), "jobs": out})
}

func (s *Server) handleScan(w http.ResponseWriter, r *http.Request) {
	var req model.ScanRequest
	if !readJSON(w, r, &req) {
		return
	}
	model.NormalizeScanRequest(&req)
	if err := model.ValidateScanRequest(req); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}
	jobID := s.startScanJob(req, "")
	writeJSON(w, http.StatusAccepted, map[string]any{
		"job_id":   jobID,
		"subnets":  req.Subnets,
		"protocol": req.Protocol,
	})
}

func (s *Server) handleListJobs(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if parsed, err := strconv.Atoi(raw); err == nil && parsed > 0 && parsed <= 500 {
			limit = parsed
		}
	}
	writeJSON(w, http.StatusOK, s.jobs.List(limit))
}

func (s *Server) handleGetJob(w http.ResponseWriter, r *http.Request) {
	job, ok := s.jobs.Get(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, job)
}

func (s *Server) handleClearJobs(w http.ResponseWriter, _ *http.Request) {
	removed := s.jobs.ClearFinished()
	writeJSON(w, http.StatusOK, map[string]int{"removed": removed})
}

func sanitizeTarget(t model.Target) model.Target {
	t.SNMP.Community = ""
	t.SNMP.V3AuthPassword = ""
	t.SNMP.V3PrivPassword = ""
	t.IPMI.Password = ""
	t.Redfish.Password = ""
	return t
}

func mergeTargetSecrets(target *model.Target, existing model.Target) {
	if target.SNMP.Community == "" {
		target.SNMP.Community = existing.SNMP.Community
	}
	if target.SNMP.V3AuthPassword == "" {
		target.SNMP.V3AuthPassword = existing.SNMP.V3AuthPassword
	}
	if target.SNMP.V3PrivPassword == "" {
		target.SNMP.V3PrivPassword = existing.SNMP.V3PrivPassword
	}
	if target.IPMI.Password == "" {
		target.IPMI.Password = existing.IPMI.Password
	}
	if target.Redfish.Password == "" {
		target.Redfish.Password = existing.Redfish.Password
	}
}

func sanitizeIPTarget(t model.IPTarget) model.IPTarget {
	t.SNMP.Community = ""
	t.SNMP.V3AuthPassword = ""
	t.SNMP.V3PrivPassword = ""
	return t
}

func mergeIPTargetSecrets(target *model.IPTarget, existing model.IPTarget) {
	if target.SNMP.Community == "" {
		target.SNMP.Community = existing.SNMP.Community
	}
	if target.SNMP.V3AuthPassword == "" {
		target.SNMP.V3AuthPassword = existing.SNMP.V3AuthPassword
	}
	if target.SNMP.V3PrivPassword == "" {
		target.SNMP.V3PrivPassword = existing.SNMP.V3PrivPassword
	}
}