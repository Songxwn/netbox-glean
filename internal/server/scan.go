package server

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Songxwn/netbox-glean/internal/collect"
	"github.com/Songxwn/netbox-glean/internal/jobs"
	"github.com/Songxwn/netbox-glean/internal/model"
)

const (
	scanConcurrency  = 16
	scanProbeTimeout = 45 * time.Second
)

type scanOutcome struct {
	ip     string
	target model.Target
	data   *model.Collected
	err    error
	status string // skipped, failed, existing
}

// startScanJob starts a background batch device scan.
func (s *Server) startScanJob(req model.ScanRequest, trigger string) string {
	meta := jobs.Meta{
		ID:       "scan-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Name:     "设备扫描 · " + req.Subnets,
		Protocol: model.ProtocolScan,
	}
	return s.jobs.StartScan(meta, trigger, func(ctx context.Context, logf model.LogFunc) (*model.ScanResult, error) {
		return s.runDeviceScan(ctx, req, logf)
	})
}

// runDeviceScan sweeps the requested subnets, probes every alive host and
// creates device targets for the hosts that answer the configured protocol.
func (s *Server) runDeviceScan(ctx context.Context, req model.ScanRequest, logf model.LogFunc) (*model.ScanResult, error) {
	if logf == nil {
		logf = func(string, ...any) {}
	}
	logf("开始设备扫描: %s，协议 %s", req.Subnets, req.Protocol)
	alive, scanned, warnings := collect.Sweep(ctx, req.ScanCIDRs(), logf)
	res := &model.ScanResult{
		Subnets:  req.Subnets,
		Protocol: req.Protocol,
		Scanned:  scanned,
		Alive:    len(alive),
		Warnings: warnings,
	}
	if len(alive) == 0 {
		res.Warnings = append(res.Warnings, "未发现 ICMP 存活主机")
		logf("扫描完成: 未发现存活主机")
		return res, nil
	}

	existingByAddress := map[string]model.Target{}
	usedNames := map[string]bool{}
	for _, target := range s.store.Targets() {
		existingByAddress[scanAddress(target.Address)] = target
		usedNames[strings.ToLower(target.Name)] = true
	}

	outcomes := make([]scanOutcome, 0, len(alive))
	var existing []scanOutcome
	var mu sync.Mutex
	sem := make(chan struct{}, scanConcurrency)
	var wg sync.WaitGroup
	for _, ip := range alive {
		if ctx.Err() != nil {
			break
		}
		if target, ok := existingByAddress[ip]; ok {
			existing = append(existing, scanOutcome{ip: ip, target: target, status: "existing"})
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(ip string) {
			defer wg.Done()
			defer func() { <-sem }()
			out := scanOutcome{ip: ip, status: "failed"}
			probeTarget := scanDeviceTarget(req, ip)
			if err := collect.Probe(ctx, probeTarget); err != nil {
				out.status = "skipped"
				out.err = err
			} else {
				probeCtx, cancel := context.WithTimeout(ctx, scanProbeTimeout)
				data, err := collect.Run(probeCtx, probeTarget, func(format string, args ...any) {
					logf("[%s] %s", ip, fmt.Sprintf(format, args...))
				})
				cancel()
				out.data = data
				out.err = err
			}
			mu.Lock()
			outcomes = append(outcomes, out)
			mu.Unlock()
		}(ip)
	}
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return res, err
	}
	outcomes = append(outcomes, existing...)

	sort.Slice(outcomes, func(i, j int) bool {
		a := net.ParseIP(outcomes[i].ip)
		b := net.ParseIP(outcomes[j].ip)
		if a == nil || b == nil {
			return outcomes[i].ip < outcomes[j].ip
		}
		return bytes.Compare(a.To16(), b.To16()) < 0
	})

	var addedTargets []model.Target
	for _, out := range outcomes {
		switch out.status {
		case "skipped":
			res.Skipped++
			res.Devices = append(res.Devices, model.ScannedDevice{
				Address: out.ip,
				Status:  "skipped",
				Error:   model.Truncate(out.err.Error(), 300),
			})
			continue
		case "existing":
			res.Existing++
			res.Devices = append(res.Devices, model.ScannedDevice{
				Address:  out.ip,
				Name:     out.target.Name,
				Status:   "existing",
				TargetID: out.target.ID,
			})
			continue
		}
		if out.err != nil {
			res.Failed++
			res.Devices = append(res.Devices, model.ScannedDevice{
				Address: out.ip,
				Status:  "failed",
				Error:   model.Truncate(out.err.Error(), 300),
			})
			continue
		}

		target := scanDeviceTarget(req, out.ip)
		target.Name = uniqueScanName(scanTargetName(req, out.ip, out.data), usedNames)
		model.NormalizeTarget(&target)
		if err := model.ValidateTarget(target); err != nil {
			res.Failed++
			res.Devices = append(res.Devices, model.ScannedDevice{
				Address: out.ip,
				Status:  "failed",
				Error:   err.Error(),
			})
			continue
		}
		saved, err := s.store.SaveTarget(target)
		if err != nil {
			res.Failed++
			res.Devices = append(res.Devices, model.ScannedDevice{
				Address: out.ip,
				Status:  "failed",
				Error:   model.Truncate(err.Error(), 300),
			})
			continue
		}
		usedNames[strings.ToLower(saved.Name)] = true
		addedTargets = append(addedTargets, saved)
		device := model.ScannedDevice{
			Address:  out.ip,
			Name:     saved.Name,
			Status:   "added",
			TargetID: saved.ID,
		}
		if out.data != nil {
			device.Manufacturer = out.data.Identity.Manufacturer
			device.Model = out.data.Identity.Model
			device.Serial = out.data.Identity.Serial
		}
		res.Devices = append(res.Devices, device)
	}
	res.Added = len(addedTargets)

	if req.Sync {
		for _, target := range addedTargets {
			s.startJobWith(target, true, jobs.TriggerScan)
		}
		if len(addedTargets) > 0 {
			logf("已为 %d 个新目标提交采集并同步任务", len(addedTargets))
		}
	}
	logf("扫描完成: 存活 %d, 新增 %d, 已存在 %d, 跳过 %d, 失败 %d",
		res.Alive, res.Added, res.Existing, res.Skipped, res.Failed)
	return res, nil
}

func scanDeviceTarget(req model.ScanRequest, ip string) model.Target {
	return model.Target{
		Name:       ip,
		Protocol:   req.Protocol,
		Address:    ip,
		Port:       req.Port,
		Enabled:    req.ScanEnabled(),
		Site:       req.Site,
		DeviceRole: req.DeviceRole,
		Platform:   req.Platform,
		SNMP:       req.SNMP,
		IPMI:       req.IPMI,
		Redfish:    req.Redfish,
	}
}

func scanTargetName(req model.ScanRequest, ip string, data *model.Collected) string {
	name := ""
	if data != nil {
		name = model.First(data.Identity.Name)
	}
	if name == "" || name == ip {
		name = strings.ReplaceAll(ip, ".", "-")
	}
	if req.NamePrefix != "" {
		name = req.NamePrefix + "-" + name
	}
	return model.Truncate(name, 64)
}

func uniqueScanName(base string, used map[string]bool) string {
	base = model.Truncate(model.First(base, "device"), 64)
	candidate := base
	for i := 2; used[strings.ToLower(candidate)]; i++ {
		suffix := fmt.Sprintf("-%d", i)
		candidate = model.Truncate(base, 64-len(suffix)) + suffix
	}
	return candidate
}

// scanAddress extracts a bare host so an existing target stored as a URL or
// with an explicit port still matches the scanned IP.
func scanAddress(address string) string {
	address = strings.TrimSpace(address)
	if strings.Contains(address, "://") {
		if u, err := url.Parse(address); err == nil && u.Hostname() != "" {
			address = u.Hostname()
		}
	}
	if host, _, err := net.SplitHostPort(address); err == nil {
		address = host
	}
	return strings.Trim(address, "[]")
}