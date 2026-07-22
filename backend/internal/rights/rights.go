// Package rights enumerates the fine-grained rights hostek declares to the holistic
// rights standard. Each constant is the Linux group backing one permission in
// permissions/hostek.json — keep the three in sync (this file ⇄ the manifest ⇄ the UI
// right constants). Enforcement uses auth.User.Can, i.e. isAdmin || group ∈ groups; a
// host without privleg has empty hp_* groups, so non-admins reduce to admin-only.
package rights

const (
	// GroupPower backs system:power — change OS power/headless + SSH-session config
	// and the Config tab. Dangerous.
	GroupPower = "hp_hostek_power"
	// GroupProc backs insight:processes — the per-process breakdown + the Processes tab.
	GroupProc = "hp_hostek_proc"
	// GroupPowerInfo backs insight:powerinfo — power telemetry (Watt) + the Power tab.
	GroupPowerInfo = "hp_hostek_powerinfo"
	// GroupThermal backs insight:thermal — temperatures (CPU/GPU/disk) + the Thermal tab.
	GroupThermal = "hp_hostek_thermal"
	// GroupTechInfo backs insight:techinfo — technical fields: power-on hours, firmware, driver.
	GroupTechInfo = "hp_hostek_techinfo"
	// GroupIdentity backs insight:hwdetail — sensitive identity fields: serial numbers, MACs.
	GroupIdentity = "hp_hostek_hwdetail"
	// GroupDisks backs insight:disks — the Disks tab (all disks).
	GroupDisks = "hp_hostek_disks"
	// GroupMount backs storage:mount — mount/unmount partitions from the Disks tab. Dangerous.
	GroupMount = "hp_hostek_mount"
	// GroupEject backs storage:eject — safely remove a whole disk (detach it). Dangerous.
	GroupEject = "hp_hostek_eject"
)
