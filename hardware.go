package main

import (
	"fmt"
	"math"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"unsafe"
)

type SystemHardwareSpecs struct {
	TotalRAMGB   float64 `json:"totalRamGb"`
	FreeRAMGB    float64 `json:"freeRamGb"`
	GPUName      string  `json:"gpuName"`
	VRAMGB       float64 `json:"vramGb"`
	FreeVRAMGB   float64 `json:"freeVramGb"`
	FreeDiskGB   float64 `json:"freeDiskGb"`
	HasNvidiaGPU bool    `json:"hasNvidiaGpu"`
}

type ModelCompatibility struct {
	ModelID         string              `json:"modelId"`
	ModelName       string              `json:"modelName"`
	TierLabel       string              `json:"tierLabel"` // "3B", "7B", "14B", "32B"
	DownloadSizeGB  float64             `json:"downloadSizeGb"`
	RequiredRAMGB   float64             `json:"requiredRamGb"`
	RecommendedVRAM float64             `json:"recommendedVramGb"`
	Verdict         string              `json:"verdict"` // "optimal", "good", "moderate", "warning"
	VerdictTitle    string              `json:"verdictTitle"`
	VerdictDetail   string              `json:"verdictDetail"`
	EstimatedSpeed  string              `json:"estimatedSpeed"`
	CanInstall      bool                `json:"canInstall"`
	Specs           SystemHardwareSpecs `json:"specs"`
}

type memStatusEx struct {
	dwLength                uint32
	dwMemoryLoad            uint32
	ullTotalPhys            uint64
	ullAvailPhys            uint64
	ullTotalPageFile        uint64
	ullAvailPageFile        uint64
	ullTotalVirtual         uint64
	ullAvailVirtual         uint64
	ullAvailExtendedVirtual uint64
}

// CheckSystemSpecs gathers real-time CPU, RAM, VRAM, and Disk information
func (a *App) CheckSystemSpecs() SystemHardwareSpecs {
	specs := SystemHardwareSpecs{
		TotalRAMGB: 16.0,
		FreeRAMGB:  8.0,
		FreeDiskGB: 50.0,
	}

	if runtime.GOOS == "windows" {
		// 1. RAM via GlobalMemoryStatusEx
		k32 := syscall.NewLazyDLL("kernel32.dll")
		procMem := k32.NewProc("GlobalMemoryStatusEx")
		var ms memStatusEx
		ms.dwLength = uint32(unsafe.Sizeof(ms))
		if r1, _, _ := procMem.Call(uintptr(unsafe.Pointer(&ms))); r1 != 0 {
			specs.TotalRAMGB = math.Round(float64(ms.ullTotalPhys)/(1024*1024*1024)*10) / 10
			specs.FreeRAMGB = math.Round(float64(ms.ullAvailPhys)/(1024*1024*1024)*10) / 10
		}

		// 2. Disk Free Space on models directory drive
		targetPath := modelsDir()
		if vol := filepath.VolumeName(targetPath); vol != "" {
			procDisk := k32.NewProc("GetDiskFreeSpaceExW")
			volUTF16, err := syscall.UTF16PtrFromString(vol + "\\")
			if err == nil {
				var freeBytesAvailable, totalBytes, totalFreeBytes uint64
				if r, _, _ := procDisk.Call(
					uintptr(unsafe.Pointer(volUTF16)),
					uintptr(unsafe.Pointer(&freeBytesAvailable)),
					uintptr(unsafe.Pointer(&totalBytes)),
					uintptr(unsafe.Pointer(&totalFreeBytes)),
				); r != 0 {
					specs.FreeDiskGB = math.Round(float64(freeBytesAvailable)/(1024*1024*1024)*10) / 10
				}
			}
		}
	}

	// 3. GPU & VRAM via nvidia-smi
	cmd := exec.Command("nvidia-smi", "--query-gpu=name,memory.total,memory.free", "--format=csv,noheader,nounits")
	hideWindow(cmd)
	if out, err := cmd.Output(); err == nil {
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) > 0 {
			parts := strings.Split(lines[0], ",")
			if len(parts) >= 3 {
				specs.HasNvidiaGPU = true
				specs.GPUName = strings.TrimSpace(parts[0])
				if totalMB, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err == nil {
					specs.VRAMGB = math.Round(totalMB/1024*10) / 10
				}
				if freeMB, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64); err == nil {
					specs.FreeVRAMGB = math.Round(freeMB/1024*10) / 10
				}
			}
		}
	}

	if specs.GPUName == "" {
		specs.GPUName = "Integrated / System Graphics"
	}

	return specs
}

// CheckModelCompatibility assesses whether the user's machine is suitable for a specific Qwen-Coder model size
func (a *App) CheckModelCompatibility(modelID string) ModelCompatibility {
	specs := a.CheckSystemSpecs()

	res := ModelCompatibility{
		ModelID:    modelID,
		Specs:      specs,
		CanInstall: true,
	}

	switch modelID {
	case "sent-code-3b", "qwen-coder-3b":
		res.ModelName = "Qwen-Coder 3B"
		res.TierLabel = "3B"
		res.DownloadSizeGB = 1.9
		res.RequiredRAMGB = 4.0
		res.RecommendedVRAM = 2.5
		res.EstimatedSpeed = "~60-80 tokens/sec"

		if specs.VRAMGB >= 2.5 {
			res.Verdict = "optimal"
			res.VerdictTitle = "ยอดเยี่ยม (Optimal)"
			res.VerdictDetail = "การ์ดจอของคุณรันโมเดลนี้ได้ใน VRAM 100% ตอบสนองเร็วระดับเสี้ยววินาที"
		} else {
			res.Verdict = "good"
			res.VerdictTitle = "ใช้งานได้ดี (Good)"
			res.VerdictDetail = "รันบน RAM เครื่องได้อย่างลื่นไหลและใช้ทรัพยากรน้อย"
		}

	case "qwen-coder", "qwen-coder-7b":
		res.ModelName = "Qwen-Coder 7B"
		res.TierLabel = "7B"
		res.DownloadSizeGB = 4.4
		res.RequiredRAMGB = 8.0
		res.RecommendedVRAM = 5.0
		res.EstimatedSpeed = "~25-35 tokens/sec"

		if specs.TotalRAMGB >= 12.0 && specs.VRAMGB >= 4.0 {
			res.Verdict = "good"
			res.VerdictTitle = "เหมาะสมมาก (Recommended)"
			res.VerdictDetail = "สเปกเครื่องของคุณรัน 7B ได้สบายๆ โดยใช้การ์ดจอควบคู่กับ RAM เครื่อง ให้ความฉลาดและแม่นยำสูงมาก"
		} else if specs.TotalRAMGB >= 8.0 {
			res.Verdict = "moderate"
			res.VerdictTitle = "พอใช้งานได้ (Moderate)"
			res.VerdictDetail = "รันได้บน RAM ระบบ อาจใช้เวลาประมวลผลเพิ่มขึ้นเล็กน้อย"
		} else {
			res.Verdict = "warning"
			res.VerdictTitle = "ไม่แนะนำ (Low Memory)"
			res.VerdictDetail = "RAM เครื่องเหลือน้อย อาจทำให้ระบบหน่วงขณะประมวลผล"
		}

	case "qwen-coder-14b":
		res.ModelName = "Qwen-Coder 14B"
		res.TierLabel = "14B"
		res.DownloadSizeGB = 9.0
		res.RequiredRAMGB = 16.0
		res.RecommendedVRAM = 10.0
		res.EstimatedSpeed = "~8-14 tokens/sec"

		if specs.TotalRAMGB >= 15.0 {
			res.Verdict = "moderate"
			res.VerdictTitle = "พอใช้งานได้ (Moderate / RAM Offload)"
			res.VerdictDetail = "โมเดลมีขนาดใหญ่เกิน VRAM การ์ดจอ จะรันโดยใช้ RAM เครื่อง 16GB เป็นหลัก ความเร็วจะอยู่ในระดับปานกลาง"
		} else {
			res.Verdict = "warning"
			res.VerdictTitle = "สเปกตึงเกินไป (High Memory Required)"
			res.VerdictDetail = "โมเดลนี้ต้องการ RAM อย่างน้อย 16 GB แนะนำให้ปิดโปรแกรมอื่นๆ ก่อนรัน"
		}

	case "qwen-coder-32b":
		res.ModelName = "Qwen-Coder 32B"
		res.TierLabel = "32B"
		res.DownloadSizeGB = 20.0
		res.RequiredRAMGB = 32.0
		res.RecommendedVRAM = 24.0
		res.EstimatedSpeed = "~2-5 tokens/sec"

		if specs.TotalRAMGB >= 30.0 {
			res.Verdict = "good"
			res.VerdictTitle = "รองรับได้ (Supported)"
			res.VerdictDetail = "เครื่องมี RAM เพียงพอสำหรับโมเดลระดับ Expert 32B"
		} else {
			res.Verdict = "warning"
			res.VerdictTitle = "ไม่แนะนำสำหรับเครื่องนี้ (Heavyweight Model)"
			res.VerdictDetail = fmt.Sprintf("โมเดล 32B ต้องการ RAM ขั้นต่ำ 24-32 GB (เครื่องนี้มี %.1f GB) หากติดตั้งอาจพบอาการกระตุกหรือ Out of Memory ได้", specs.TotalRAMGB)
		}

	default:
		res.ModelName = modelID
		res.TierLabel = "Custom"
		res.DownloadSizeGB = 4.0
		res.RequiredRAMGB = 8.0
		res.Verdict = "good"
		res.VerdictTitle = "สามารถติดตั้งได้"
		res.VerdictDetail = "สเปกเครื่องพร้อมสำหรับการติดตั้งโมเดลนี้"
	}

	// Check disk space
	if specs.FreeDiskGB < res.DownloadSizeGB+2.0 {
		res.CanInstall = false
		res.Verdict = "warning"
		res.VerdictTitle = "พื้นที่ฮาร์ดดิสก์ไม่เพียงพอ"
		res.VerdictDetail = fmt.Sprintf("ต้องการพื้นที่ว่างอย่างน้อย %.1f GB แต่เหลือเพียง %.1f GB กรุณาเคลียร์พื้นที่ก่อน", res.DownloadSizeGB+2.0, specs.FreeDiskGB)
	}

	return res
}
