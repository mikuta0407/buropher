// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package domain

// LastnameBeforeFirstname は User.lastname_before_firstname?(formatter)（Redmine 7.0 #4507）。
// 表示形式（USER_FORMATS[:order]）が firstname と lastname の両方を含み、lastname が先なら true。
// 未知の形式は firstname_lastname 扱い（false）。
func LastnameBeforeFirstname(format string) bool {
	switch format {
	case "lastname_firstname", "lastnamefirstname", "lastname_comma_firstname":
		return true
	}
	return false
}
