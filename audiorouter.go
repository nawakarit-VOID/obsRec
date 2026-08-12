// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import (
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
)

// ชื่อ virtual sink ที่ OBS จะ capture เสียงจาก monitor ของมัน
const sinkName = "OBS_Record"

// SinkInput เก็บข้อมูล audio stream หนึ่งตัวจาก `pactl list sink-inputs`
type SinkInput struct {
	ID        int
	SinkIndex int               // index ของ sink ปลายทางที่ stream นี้กำลังต่ออยู่ตอนนี้
	Corked    bool              // true = หยุดเล่นชั่วคราว (paused/ไม่มีเสียงออกตอนนี้)
	Props     map[string]string // property ทั้งหมด เช่น application.name, media.title, application.process.id
}

// DisplayLabel รวม property ที่น่าจะบอกได้ว่าเป็นแหล่งเสียงไหน
// ลำดับความสำคัญ: media.title > media.name (ถ้า Firefox/แอปไม่ส่งชื่อแท็บมา
// จะเหลือแค่ "Playback" เฉยๆ ซึ่งเป็นข้อจำกัดของแอปนั้น ไม่ใช่บั๊กโปรแกรม)
func (si SinkInput) DisplayLabel() string {
	appName := si.Props["application.name"]
	if appName == "" {
		appName = "Unknown"
	}
	title := si.Props["media.title"]
	if title == "" {
		title = si.Props["media.name"]
	}

	label := appName
	if title != "" && title != appName {
		label = fmt.Sprintf("%s — %s", appName, title)
	}
	if pid := si.Props["application.process.id"]; pid != "" {
		label = fmt.Sprintf("%s [pid %s]", label, pid)
	}
	return label
}

func runCmd(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// getSinkIndex หา index ตัวเลขของ sink จากชื่อ (จาก `pactl list sinks short`)
func getSinkIndex(name string) (int, error) {
	out, err := runCmd("pactl", "list", "sinks", "short")
	if err != nil {
		return -1, err
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		if fields[1] == name {
			idx, err := strconv.Atoi(fields[0])
			if err != nil {
				return -1, err
			}
			return idx, nil
		}
	}
	return -1, fmt.Errorf("ไม่พบ sink ชื่อ %s", name)
}

func sinkExists() bool {
	_, err := getSinkIndex(sinkName)
	return err == nil
}

// ensureSink สร้าง null-sink ถ้ายังไม่มี (idempotent)
// ใส่ node.autoconnect=false ไว้ด้วย เพื่อบอก WirePlumber ว่าห้ามเลือก sink นี้เป็น
// เป้าหมายอัตโนมัติเด็ดขาด (ทั้งจาก default policy และ stream-restore memory)
// ให้ stream ใหม่ไปลงที่ default sink เสมอ จนกว่าโปรแกรมนี้จะสั่งย้ายเอง
func ensureSink() error {
	if sinkExists() {
		return nil
	}
	_, err := runCmd("pactl", "load-module", "module-null-sink",
		fmt.Sprintf("sink_name=%s", sinkName),
		fmt.Sprintf("sink_properties=device.description=%s node.autoconnect=false", sinkName))
	return err
}

var sinkInputHeaderRe = regexp.MustCompile(`(?m)^Sink Input #(\d+)`)
var sinkIndexRe = regexp.MustCompile(`(?m)^\s*Sink:\s*(\d+)`)
var mediaNameLineRe = regexp.MustCompile(`(?m)^\s*Media Name:\s*"([^"]*)"`)
var propLineRe = regexp.MustCompile(`(?m)^\s*([\w.]+)\s*=\s*"([^"]*)"`)
var corkedRe = regexp.MustCompile(`(?m)^\s*Corked:\s*(yes|no)`)

// listSinkInputs อ่าน audio stream ทั้งหมดที่กำลังเล่นอยู่ตอนนี้
func listSinkInputs() ([]SinkInput, error) {
	out, err := runCmd("pactl", "list", "sink-inputs")
	if err != nil {
		return nil, err
	}
	idxs := sinkInputHeaderRe.FindAllStringSubmatchIndex(out, -1)
	var result []SinkInput
	for i, loc := range idxs {
		start := loc[0]
		end := len(out)
		if i+1 < len(idxs) {
			end = idxs[i+1][0]
		}
		block := out[start:end]

		id, _ := strconv.Atoi(out[loc[2]:loc[3]])
		si := SinkInput{ID: id, SinkIndex: -1, Props: map[string]string{}}
		if sm := sinkIndexRe.FindStringSubmatch(block); sm != nil {
			si.SinkIndex, _ = strconv.Atoi(sm[1])
		}
		for _, pm := range propLineRe.FindAllStringSubmatch(block, -1) {
			si.Props[pm[1]] = pm[2]
		}
		if mm := mediaNameLineRe.FindStringSubmatch(block); mm != nil {
			if _, ok := si.Props["media.name"]; !ok {
				si.Props["media.name"] = mm[1]
			}
		}
		if cm := corkedRe.FindStringSubmatch(block); cm != nil {
			si.Corked = cm[1] == "yes"
		}
		result = append(result, si)
	}
	return result, nil
}

// moveSinkInput ย้าย audio stream ไปยัง sink ปลายทาง (ระบุด้วยชื่อ)
func moveSinkInput(id int, sink string) error {
	_, err := runCmd("pactl", "move-sink-input", strconv.Itoa(id), sink)
	return err
}

// รูปแบบบรรทัดของ `wmctrl -lx`:
// <window_id> <desktop> <WM_CLASS> <hostname> <title...>
var wmctrlLineRe = regexp.MustCompile(`^(\S+)\s+(-?\d+)\s+(\S+)\s+(\S+)\s+(.*)$`)

// findPiPWindowPID มองหาหน้าต่าง Picture-in-Picture ของ Firefox
// สังเกตจาก WM_CLASS ที่ขึ้นต้นด้วย "Toolkit" (คงที่ไม่ว่า UI จะภาษาอะไร)
func findPiPWindowPID() (int, bool, error) {
	out, err := runCmd("wmctrl", "-lx")
	if err != nil {
		return 0, false, err
	}
	for _, line := range strings.Split(out, "\n") {
		m := wmctrlLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		windowID, class := m[1], m[3]
		if !strings.Contains(strings.ToLower(class), "toolkit") {
			continue
		}
		pidOut, err := runCmd("xdotool", "getwindowpid", windowID)
		if err != nil {
			continue
		}
		pid, err := strconv.Atoi(strings.TrimSpace(pidOut))
		if err != nil {
			continue
		}
		return pid, true, nil
	}
	return 0, false, nil
}
