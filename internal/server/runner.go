package server

import (
	"context"

	"github.com/Songxwn/netbox-glean/internal/collect"
	"github.com/Songxwn/netbox-glean/internal/model"
	"github.com/Songxwn/netbox-glean/internal/netbox"
)

func (s *Server) startJob(target model.Target, sync bool) string {
	mode := model.ModeCollect
	if sync {
		mode = model.ModeSync
	}
	settings := s.store.Settings()
	return s.jobs.Start(target, mode, func(ctx context.Context, logf model.LogFunc) (*model.Collected, *model.SyncResult, error) {
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