package main

import (
	"bytes"
	"fmt"
	"runtime"
	"strconv"
	"strings"
)

type refFormat uint8

const (
	refName           refFormat = iota // name of the block device, e.g. "sda". It corresponds to path inside /dev/ directory.
	refGptType                         // type of gpt partition
	refGptUuid                         // uuid of the gpt partition
	refGptUuidPartoff                  // offset against a gpt partition with uuid
	refGptLabel
	refFsUuid
	refFsLabel
)

// The are many ways a user can specify root partition (using name, fs uuid, fs label, gpt attribute, ...).
// This struct abstracts this information and provides a convenient matching functions.
type deviceRef struct {
	format refFormat
	data   interface{}
}

type gptPartoffData struct {
	uuid   UUID
	offset int
}

func (d *deviceRef) matchesName(name string) bool {
	return d.format == refName && d.data.(string) == name
}

func (d *deviceRef) matchesBlkInfo(info *blkInfo) bool {
	switch d.format {
	case refFsUuid:
		return bytes.Equal(d.data.(UUID), info.uuid)
	case refFsLabel:
		return d.data.(string) == info.label
	default:
		return false
	}
}

// checks if the reference is a gpt-specific and if yes then tries to resolve it to a device name
func (d *deviceRef) resolveFromGptTable(devName string, t []gptPart) *deviceRef {
	if d.format != refGptType && d.format != refGptUuid && d.format != refGptLabel && d.format != refGptUuidPartoff {
		return d
	}

	calculateDevName := func(parent string, partition int) string {
		name := parent
		// some drivers use 'p' prefix for the partition number. TODO: find out where it is codified.
		if strings.HasPrefix(parent, "nvme") || strings.HasPrefix(parent, "mmcblk") {
			name += "p"
		}
		name += strconv.Itoa(partition + 1) // devname partitions start with "1"
		return name
	}

	for _, p := range t {
		switch d.format {
		case refGptType:
			if bytes.Equal(d.data.(UUID), p.typeGuid) {
				return &deviceRef{refName, calculateDevName(devName, p.num)}
			}
		case refGptUuid:
			if bytes.Equal(d.data.(UUID), p.uuid) {
				return &deviceRef{refName, calculateDevName(devName, p.num)}
			}
		case refGptUuidPartoff:
			data := d.data.(gptPartoffData)
			if bytes.Equal(data.uuid, p.uuid) {
				return &deviceRef{refName, calculateDevName(devName, p.num+data.offset)}
			}
		case refGptLabel:
			if d.data.(string) == p.name {
				return &deviceRef{refName, calculateDevName(devName, p.num)}
			}
		}
	}

	return d
}

var autodiscoveryGptTypes = map[string]string{
	"amd64": "4f68bce3-e8cd-4db1-96e7-fbcaf984b709",
	"386":   "44479540-f297-41b2-9af7-d131d5f0458a",
	"arm":   "69dad710-2ce4-4e3c-b16c-21a1d49abed3",
	"arm64": "b921b045-1df0-41c3-af44-4c6f280d3fae",
	//"itanium": "993d8d3d-f80e-4225-855a-9daf8ed7ea97",
}

func parseDeviceRef(name, param string, enableAutodetect bool) (*deviceRef, error) {
	if param == "" {
		// try to auto-discover gpt partition https://www.freedesktop.org/wiki/Specifications/DiscoverablePartitionsSpec/
		if autodiscoveryGuid, ok := autodiscoveryGptTypes[runtime.GOARCH]; enableAutodetect && ok {
			debug("%s= param is not specified. Use GPT partition autodiscovery with guid type %s", name, autodiscoveryGuid)
			gptType, err := parseUUID(autodiscoveryGuid)
			if err != nil {
				return nil, err
			}
			return &deviceRef{refGptType, gptType}, nil
		}
		return nil, fmt.Errorf("%s= boot option is not specified", name)
	}

	if strings.HasPrefix(param, "UUID=") {
		uuid := strings.TrimPrefix(param, "UUID=")

		u, err := parseUUID(stripQuotes(uuid))
		if err != nil {
			return nil, fmt.Errorf("unable to parse UUID parameter %s: %v", param, err)
		}
		return &deviceRef{refFsUuid, u}, nil

	}
	if strings.HasPrefix(param, "/dev/disk/by-uuid/") {
		uuid := strings.TrimPrefix(param, "/dev/disk/by-uuid/")
		u, err := parseUUID(stripQuotes(uuid))
		if err != nil {
			return nil, fmt.Errorf("unable to parse UUID parameter %s: %v", param, err)
		}
		return &deviceRef{refFsUuid, u}, nil
	}
	if strings.HasPrefix(param, "LABEL=") {
		label := strings.TrimPrefix(param, "LABEL=")
		return &deviceRef{refFsLabel, label}, nil
	}
	if strings.HasPrefix(param, "/dev/disk/by-label/") {
		label := strings.TrimPrefix(param, "/dev/disk/by-label/")
		return &deviceRef{refFsLabel, label}, nil
	}

	if strings.HasPrefix(param, "PARTUUID=") {
		uuid := strings.TrimPrefix(param, "PARTUUID=")

		if idx := strings.Index(uuid, "/PARTNROFF="); idx != -1 {
			param := uuid[idx+11:]
			uuid = uuid[:idx]
			partnoff, err := strconv.Atoi(param)
			if err != nil {
				return nil, fmt.Errorf("unable to parse PARTNROFF= value %s", param)
			}
			u, err := parseUUID(stripQuotes(uuid))
			if err != nil {
				return nil, fmt.Errorf("unable to parse UUID parameter %s: %v", param, err)
			}
			return &deviceRef{refGptUuidPartoff, gptPartoffData{u, partnoff}}, nil
		} else {
			u, err := parseUUID(stripQuotes(uuid))
			if err != nil {
				return nil, fmt.Errorf("unable to parse UUID parameter %s: %v", param, err)
			}
			return &deviceRef{refGptUuid, u}, nil
		}
	}
	if strings.HasPrefix(param, "/dev/disk/by-partuuid/") {
		uuid := strings.TrimPrefix(param, "/dev/disk/by-partuuid/")
		u, err := parseUUID(stripQuotes(uuid))
		if err != nil {
			return nil, fmt.Errorf("unable to parse UUID parameter %s: %v", param, err)
		}
		return &deviceRef{refGptUuid, u}, nil
	}
	if strings.HasPrefix(param, "PARTLABEL=") {
		label := strings.TrimPrefix(param, "PARTLABEL=")
		return &deviceRef{refGptLabel, label}, nil
	}
	if strings.HasPrefix(param, "/dev/disk/by-partlabel/") {
		label := strings.TrimPrefix(param, "/dev/disk/by-partlabel/")
		return &deviceRef{refGptLabel, label}, nil
	}

	if strings.HasPrefix(param, "/dev/") {
		name := strings.TrimPrefix(param, "/dev/")
		return &deviceRef{refName, name}, nil
	}

	return nil, fmt.Errorf("unable to parse %s= parameter '%s'", name, param)
}

