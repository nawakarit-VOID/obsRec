// Copyright (c) 2026 Nawakarit
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License v3.0.
package main

import "time"

// ==== แก้ค่าพวกนี้ให้ตรงกับเครื่องของคุณ ====

const (
	// ที่อยู่ + password ของ OBS WebSocket (Tools -> WebSocket Server Settings)
	obsHost     = "localhost:4455"
	obsPassword = "your_password"

	// ชื่อ pipewire monitor source ที่จะฟังเสียง เพื่อตรวจจับความเงียบ
	// หาได้ด้วยคำสั่ง: pactl list sources short
	// ควรเป็น monitor ของ sink/aux ที่คุณ route เสียง Firefox -> OBS ไว้ใน qpwgraph/wireplumber
	// ตัวอย่าง: "OBS_Aux.monitor" หรือชื่อ null-sink ที่คุณสร้างไว้
	audioMonitorSource = "OBS_Aux.monitor"

	// ความดังที่ถือว่า "เงียบ" (0.0 - 1.0, ค่าน้อย = ไวต่อเสียงเบา)
	silenceAmplitudeThreshold = 0.01

	// ต้องเงียบต่อเนื่องนานเท่านี้ ถึงจะเริ่มนับถอยหลังเพื่อหยุดอัด
	silenceDurationToTrigger = 8 * time.Second

	// เวลานับถอยหลังใน popup ก่อนหยุดอัดจริง (ให้โอกาสกดยกเลิก)
	stopCountdownDuration = 10 * time.Second
)
