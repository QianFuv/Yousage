package desktop

// GetAutostart reports whether this executable is registered to launch at login.
func (service *App) GetAutostart() (bool, error) {
	return service.nativeApp.Autostart.IsEnabled()
}

// SetAutostart registers or removes the login launch and returns the resulting registration state.
func (service *App) SetAutostart(isEnabled bool) (bool, error) {
	autostart := service.nativeApp.Autostart
	var err error
	if isEnabled {
		err = autostart.Enable()
	} else {
		err = autostart.Disable()
	}
	if err != nil {
		return false, err
	}
	return autostart.IsEnabled()
}
