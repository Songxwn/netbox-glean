package collect

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	ipmiclient "github.com/bougou/go-ipmi/pkg/client"
	"github.com/bougou/go-ipmi/pkg/types"
	"github.com/Songxwn/netbox-glean/internal/model"
)

// collectIPMI reads the FRU inventory over IPMI 2.0 (RMCP+) without requiring
// an external ipmitool binary. The pure Go client is cross-platform.
func collectIPMI(ctx context.Context, target model.Target, logf model.LogFunc) (*model.Collected, error) {
	port := target.Port
	if port == 0 {
		port = 623
	}
	client, err := ipmiclient.NewClient(target.Address, port, target.IPMI.Username, target.IPMI.Password)
	if err != nil {
		return nil, fmt.Errorf("创建 IPMI 客户端失败: %w", err)
	}
	if strings.EqualFold(target.IPMI.Interface, "lan") {
		client.WithInterface(ipmiclient.InterfaceLan)
	}
	logf("IPMI 连接 %s:%d (%s)", target.Address, port, target.IPMI.Interface)
	if err := client.Connect(ctx); err != nil {
		return nil, fmt.Errorf("IPMI 连接 %s:%d 失败: %w", target.Address, port, err)
	}
	defer func() {
		_ = client.Close(ctx)
	}()

	frus, fruErr := client.GetFRUs(ctx)
	out := &model.Collected{
		Source:      model.ProtocolIPMI,
		CollectedAt: time.Now().UTC(),
		Extra:       map[string]string{},
	}
	if fruErr != nil {
		out.Warnings = append(out.Warnings, "IPMI FRU 读取部分失败: "+fruErr.Error())
		logf("IPMI FRU 读取部分失败: %v", fruErr)
	}
	if len(frus) == 0 {
		if fruErr != nil {
			return nil, fmt.Errorf("未读取到 IPMI FRU 信息: %w", fruErr)
		}
		return nil, fmt.Errorf("未读取到 IPMI FRU 信息")
	}

	var identitySet bool
	present := 0
	for _, fru := range frus {
		if fru == nil || !fru.Present() {
			continue
		}
		present++
		info := decodeFRU(fru)
		if !identitySet {
			id := &out.Identity
			id.Manufacturer = model.First(info.manufacturer, info.boardManufacturer)
			id.Model = model.First(info.productName, info.partModel, info.boardProduct, info.boardPart)
			id.Serial = model.First(info.serial, info.boardSerial, info.chassisSerial)
			id.AssetTag = info.assetTag
			if info.productVersion != "" {
				out.Extra["product_version"] = info.productVersion
			}
			if info.chassisType != "" {
				out.Extra["chassis_type"] = info.chassisType
			}
			if info.chassisPart != "" {
				out.Extra["chassis_part_number"] = info.chassisPart
			}
			if id.Manufacturer != "" || id.Model != "" || id.Serial != "" {
				identitySet = true
			}
		}

		name := model.First(fru.DeviceName, info.productName, info.boardProduct, info.chassisPart)
		if name == "" {
			name = "FRU " + strconv.Itoa(int(fru.DeviceID))
		}
		class := "module"
		if fru.DeviceID == 0 {
			class = "chassis"
			name = model.First(info.productName, info.boardProduct, "System FRU")
		}
		out.Components = append(out.Components, model.Component{
			Name:         name,
			Class:        class,
			Manufacturer: model.First(info.manufacturer, info.boardManufacturer),
			Model:        model.First(info.productName, info.partModel, info.boardProduct, info.boardPart),
			Serial:       model.First(info.serial, info.boardSerial, info.chassisSerial),
			AssetTag:     info.assetTag,
			PartNumber:   model.First(info.partModel, info.boardPart, info.chassisPart),
			Description:  model.First(info.productName, info.boardProduct, info.chassisType),
			Firmware:     "",
		})
	}
	out.Extra["fru_count"] = strconv.Itoa(len(frus))
	if present == 0 {
		if fruErr != nil {
			return nil, fmt.Errorf("未读取到可用的 IPMI FRU 信息: %w", fruErr)
		}
		return nil, fmt.Errorf("未读取到可用的 IPMI FRU 信息")
	}
	logf("IPMI 采集完成: FRU %d, 可用 %d, 组件 %d", len(frus), present, len(out.Components))
	return out, nil
}

type fruDetails struct {
	manufacturer      string
	productName       string
	partModel         string
	serial            string
	assetTag          string
	productVersion    string
	boardManufacturer string
	boardProduct      string
	boardPart         string
	boardSerial       string
	chassisType       string
	chassisPart       string
	chassisSerial     string
}

func decodeFRU(fru *types.FRU) fruDetails {
	var d fruDetails
	if p := fru.ProductInfoArea; p != nil {
		d.manufacturer = types.FRUFieldString(p.ManufacturerTypeLength, p.Manufacturer)
		d.productName = types.FRUFieldString(p.NameTypeLength, p.Name)
		d.partModel = types.FRUFieldString(p.PartModelTypeLength, p.PartModel)
		d.productVersion = types.FRUFieldString(p.VersionTypeLength, p.Version)
		d.serial = types.FRUFieldString(p.SerialNumberTypeLength, p.SerialNumber)
		d.assetTag = types.FRUFieldString(p.AssetTagTypeLength, p.AssetTag)
	}
	if b := fru.BoardInfoArea; b != nil {
		d.boardManufacturer = types.FRUFieldString(b.ManufacturerTypeLength, b.Manufacturer)
		d.boardProduct = types.FRUFieldString(b.ProductNameTypeLength, b.ProductName)
		d.boardPart = types.FRUFieldString(b.PartNumberTypeLength, b.PartNumber)
		d.boardSerial = types.FRUFieldString(b.SerialNumberTypeLength, b.SerialNumber)
	}
	if c := fru.ChassisInfoArea; c != nil {
		d.chassisType = c.ChassisType.String()
		d.chassisPart = types.FRUFieldString(c.PartNumberTypeLength, c.PartNumber)
		d.chassisSerial = types.FRUFieldString(c.SerialNumberTypeLength, c.SerialNumber)
	}
	return d
}