package manager

// Related compares publisher-declared identities, never filenames or display names.
func (a App) Related(other App) bool {
	if a.ID == other.ID || a.Metadata.Identity == "" || a.Metadata.Identity != other.Metadata.Identity {
		return false
	}
	if a.Metadata.IdentitySource == "appstream" && other.Metadata.IdentitySource == "appstream" {
		return true
	}
	return a.Metadata.IdentitySource == "appimage" && other.Metadata.IdentitySource == "appimage" && a.UpdateInfo != "" && a.UpdateInfo == other.UpdateInfo
}
