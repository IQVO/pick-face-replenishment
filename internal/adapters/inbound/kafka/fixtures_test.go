package kafka_test

import (
	"strconv"
	"strings"
)

// The consumed messages are the producers' own contracts as published in their
// AsyncAPI (and pinned in ADR 0003), restated here as raw JSON, never built
// with this service's helper, which only produces this service's own types.

func ce(id, source, typ, subject, dataschema, data string) []byte {
	return []byte(`{"specversion":"1.0","id":"` + id + `","source":"` + source + `","type":"` + typ + `","subject":"` + subject + `",` +
		`"time":"2026-10-17T09:00:00Z","datacontenttype":"application/json","dataschema":"` + dataschema + `","data":` + data + `}`)
}

func inventoryMsg(id, typ, subject, data string) []byte {
	name := typ[strings.LastIndex(typ, ".")+1:]
	return ce(id, "/warehouse/inventory-storage", typ, subject, "urn:warehouse:inventory-storage:events:"+name+":v1", data)
}

const (
	typeBinLevel  = "com.warehouse.wms.inventory-storage.stock.BinStockLevelChanged"
	typeRelocated = "com.warehouse.wms.inventory-storage.stock.StockRelocated"
	typeSlotPlan  = "com.warehouse.wms.slotting-optimization.slotplan.SlotPlanApproved"
	typeZone      = "com.warehouse.wms.facility-layout.zone.ZoneRegistered"
	typeLocation  = "com.warehouse.wms.facility-layout.locationslot.LocationSlotRegistered"
	typeDecom     = "com.warehouse.wms.facility-layout.locationslot.LocationSlotDecommissioned"
	typeGeometry  = "com.warehouse.wms.facility-layout.locationslot.LocationGeometryUpdated"
)

func binLevelMsg(id, bin, sku string, onHand, reserved, version int) []byte {
	data := `{"site_code":"S1","bin":"` + bin + `","sku":"` + sku + `","on_hand":` + strconv.Itoa(onHand) + `,"reserved":` + strconv.Itoa(reserved) +
		`,"version":` + strconv.Itoa(version) + `,"reason":"PICK","occurred_at":"2026-10-17T09:00:00Z"}`
	return inventoryMsg(id, typeBinLevel, bin+":"+sku, data)
}

func relocatedMsg(id, requestID, sku, from, to string, quantity int, reason string) []byte {
	data := `{"relocation_id":"rel-1","site_code":"S1","sku":"` + sku + `","from_bin":"` + from + `","to_bin":"` + to + `","quantity":` + strconv.Itoa(quantity) +
		`,"reason":"` + reason + `","source_ref":"` + requestID + `","occurred_at":"2026-10-17T10:00:00Z"}`
	return inventoryMsg(id, typeRelocated, "rel-1", data)
}

// slotPlanMsg builds a SlotPlanApproved whose assignments are "SKU=slot" pairs.
func slotPlanMsg(id, planID, approvedAt string, assignments ...string) []byte {
	entries := make([]string, len(assignments))
	for i, a := range assignments {
		sku, slot, _ := strings.Cut(a, "=")
		entries[i] = `{"sku":"` + sku + `","slot":"` + slot + `"}`
	}
	data := `{"plan_id":"` + planID + `","site_id":"S1","approved_at":"` + approvedAt + `","supersedes_plan_id":"plan-0",` +
		`"assignments":[` + strings.Join(entries, ",") + `],"moves":[{"sku":"SKU-1","to_slot":"F-01","kind":"Assign"}]}`
	return ce(id, "/warehouse/slotting-optimization", typeSlotPlan, planID, "urn:warehouse:slotting-optimization:events:SlotPlanApproved:v1", data)
}

func facilityMsg(id, typ, subject, data string) []byte {
	name := typ[strings.LastIndex(typ, ".")+1:]
	return ce(id, "/warehouse/facility-layout", typ, subject, "urn:warehouse:facility-layout:events:"+name+":v1", data)
}

func zoneMsg(id, zoneID, zoneCode string) []byte {
	return facilityMsg(id, typeZone, zoneID, `{"zoneId":"`+zoneID+`","siteCode":"S1","areaCode":"A1","zoneCode":"`+zoneCode+`","temperatureClass":"Ambient","hazmat":false}`)
}

func locationMsg(id, code, zoneID string) []byte {
	return facilityMsg(id, typeLocation, code, `{"locationCode":"`+code+`","aisleId":"A-01","zoneId":"`+zoneID+`","locationType":"Slot","maxWeightKg":40,"maxVolumeM3":0.5}`)
}

func decommissionedMsg(id, code string) []byte {
	return facilityMsg(id, typeDecom, code, `{"locationCode":"`+code+`"}`)
}

// legacyFlat is the retired flat envelope; a consumer must reject it.
const legacyFlat = `{"event_id":"e-1","event_type":"BinStockLevelChanged","occurred_at":"2026-10-17T09:00:00Z","site_code":"S1","bin":"F-01","sku":"SKU-1","on_hand":8}`
