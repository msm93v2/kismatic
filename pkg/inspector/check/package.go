package check

import (
	"fmt"
)

// PackageQuery is a query for finding a package
type PackageQuery struct {
	Name    string
	Version string
}

// The PackageCheck uses the operating system to determine whether a
// package is installed.
type PackageCheck struct {
	PackageQuery         PackageQuery
	ShouldNotBeInstalled bool
	PackageManager       PackageManager
	InstallationDisabled bool
}

// Check returns true if the package is installed. If pkg installation is disabled,
// we would like to check if the package is available for install. However,
// there is no guarantee that the node will have the kismatic package repo configured.
// For this reason, this check is a no-op when package installation is disabled.
func (c PackageCheck) Check() (bool, error) {
	installed, err := c.PackageManager.IsInstalled(c.PackageQuery)
	if err != nil {
		return false, fmt.Errorf("failed to determine if package is installed: %v", err)
	}
	if !c.InstallationDisabled {
		if installed && c.ShouldNotBeInstalled {
			return false, fmt.Errorf("package should not be installed")
		}
		return true, nil
	}
	// When installation is disabled, dont check what packages are installed
	if c.ShouldNotBeInstalled {
		return true, nil
	}
	if installed {
		return true, nil
	}
	// We check to see if it's available to give useful feedback to the user
	available, err := c.PackageManager.IsAvailable(c.PackageQuery)
	if err != nil {
		return false, fmt.Errorf("failed to determine if package is available for install: %v", err)
	}
	if !available {
		return false, fmt.Errorf("package is not installed, and is not available in known package repositories")
	}
	return false, fmt.Errorf("package is not installed, but is available in a package repository")
}
