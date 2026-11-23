package server

import (
	"context"
	"strings"

	"github.com/Songxwn/netbox-glean/internal/cloud"
	"github.com/Songxwn/netbox-glean/internal/collect"
	"github.com/Songxwn/netbox-glean/internal/jobs"
	"github.com/Songxwn/netbox-glean/internal/model"
	"github.com/Songxwn/netbox-glean/internal/netbox"
)

// startJob starts a manually triggered job (web UI or API).
func (s *Server) startJob(target model.Target, sync bool) string {
	return s.startJobWith(target, sync, jobs.TriggerManual)
}

// startIPJob starts a manually triggered standalone IP discovery job.
func (s *Server) startIPJob(target model.IPTarget, sync bool) string {
	return s.startIPJobWith(target, sync, jobs.TriggerManual)
}

// startIPJobWith starts an IP discovery job and records what triggered it.
func (s *Server) startIPJobWith(target model.IPTarget, sync bool, trigger string) string {
	mode := model.ModeCollect
	if sync {
		mode = model.ModeSync
	}
	settings := s.store.Settings()
	meta := jobs.Meta{ID: target.ID, Name: target.Name, Protocol: model.ProtocolIPScan}
	return s.jobs.StartMeta(meta, mode, trigger, func(ctx context.Context, logf model.LogFunc) (*model.Collected, *model.SyncResult, error) {
		if trigger == jobs.TriggerSchedule {
			logf("定时任务: 每 %d 分钟", int(target.ScheduleInterval().Minutes()))
		}
		logf("IP 采集目标: %s", target.Name)
		if target.Address != "" {
			logf("SNMP 来源: %s:%d", target.Address, target.Port)
		}
		if subnets := target.ScanCIDRs(); len(subnets) > 0 {
			logf("扫描网段: %s", strings.Join(subnets, ", "))
		}
		data, err := collect.RunIPDiscovery(ctx, target, logf)
		if err != nil {
			return nil, nil, err
		}
		logf("IP 采集完成: 发现 %d 个地址", len(data.IPAddresses))
		if !sync {
			logf("预览模式：未写入 NetBox")
			return data, nil, nil
		}
		result, err := netbox.SyncIPs(ctx, settings, target.Name, data.IPAddresses, logf)
		if err != nil {
			return data, nil, err
		}
		return data, result, nil
	})
}

// startCloudJob starts a manually triggered cloud asset discovery job.
func (s *Server) startCloudJob(target model.CloudTarget, sync bool) string {
	return s.startCloudJobWith(target, sync, jobs.TriggerManual)
}

// startCloudJobWith starts a cloud job and records what triggered it.
func (s *Server) startCloudJobWith(target model.CloudTarget, sync bool, trigger string) string {
	mode := model.ModeCollect
	if sync {
		mode = model.ModeSync
	}
	settings := s.store.Settings()
	meta := jobs.Meta{ID: target.ID, Name: target.Name, Protocol: model.ProtocolCloud}
	return s.jobs.StartCloud(meta, mode, trigger, func(ctx context.Context, logf model.LogFunc) (*model.CloudResult, *model.SyncResult, error) {
		if trigger == jobs.TriggerSchedule {
			logf("定时任务: 每 %d 分钟", int(target.ScheduleInterval().Minutes()))
		}
		logf("云资产目标: %s (%s)", target.Name, model.CloudProviderLabel(target.Provider))
		if target.Region != "" {
			logf("区域: %s", target.Region)
		}
		data, err := cloud.Run(ctx, target, logf)
		if err != nil {
			return nil, nil, err
		}
		if !sync {
			logf("预览模式：未写入 NetBox")
			return data, nil, nil
		}
		result, err := netbox.SyncCloud(ctx, settings, target, data, logf)
		if err != nil {
			return data, nil, err
		}
		return data, result, nil
	})
}

// startJobWith starts a job and records what triggered it.
func (s *Server) startJobWith(target model.Target, sync bool, trigger string) string {
	mode := model.ModeCollect
	if sync {
		mode = model.ModeSync
	}
	settings := s.store.Settings()
	return s.jobs.Start(target, mode, trigger, func(ctx context.Context, logf model.LogFunc) (*model.Collected, *model.SyncResult, error) {
		if trigger == jobs.TriggerSchedule {
			logf("定时任务: 每 %d 分钟", int(target.ScheduleInterval().Minutes()))
		}
		logf("目标: %s", target.Name)
		logf("协议: %s, 地址: %s:%d", target.Protocol, target.Address, target.Port)
		data, err := collect.Run(ctx, target, logf)
		if err != nil {
			return nil, nil, err
		}
		logf("采集完成: 接口 %d, 组件 %d, 警告 %d", len(data.Interfaces), len(data.Components), len(data.Warnings))
		if !sync {
			logf("预览模式：未写入 NetBox")
			return data, nil, nil
		}
		result, err := netbox.Sync(ctx, settings, target, data, logf)
		if err != nil {
			return data, nil, err
		}
		return data, result, nil
	})
}
