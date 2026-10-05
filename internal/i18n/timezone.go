// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

package i18n

//go:generate sh -c "SECRET_KEY_BASE=x RAILS_ENV=production ../../_reference/redmine702-migrated/bin/rails runner \"$(pwd)/gen_timezones.rb\" > zones_gen.go.tmp && mv zones_gen.go.tmp zones_gen.go && gofmt -w zones_gen.go"

import (
	"fmt"
	"github.com/mikuta0407/buropher/internal/clock"
	"strings"
	"sync"
	"time"

	// サーバに zoneinfo が無くてもタイムゾーンを解決できるよう tzdata を埋め込む
	_ "time/tzdata"
)

// TimeZone は ActiveSupport::TimeZone（ユーザー設定 pref.time_zone の値）に相当する。
type TimeZone struct {
	Name       string // ActiveSupport の名前（例: "Tokyo"）。pref.time_zone に保存される値
	Identifier string // IANA タイムゾーン名（例: "Asia/Tokyo"）
	UTCOffset  int    // 基準 UTC オフセット（秒）
}

// FormattedOffset は formatted_offset（例: "+09:00"）。
func (z TimeZone) FormattedOffset() string {
	return SecondsToUTCOffset(z.UTCOffset, true)
}

// String は to_s（例: "(GMT+09:00) Tokyo"）。
func (z TimeZone) String() string {
	return "(GMT" + z.FormattedOffset() + ") " + z.Name
}

// Location は IANA 名から *time.Location を返す。
func (z TimeZone) Location() (*time.Location, error) {
	return loadLocation(z.Identifier)
}

// SecondsToUTCOffset は ActiveSupport::TimeZone.seconds_to_utc_offset。
func SecondsToUTCOffset(seconds int, colon bool) string {
	sign := "+"
	if seconds < 0 {
		sign = "-"
		seconds = -seconds
	}
	h, m := seconds/3600, (seconds%3600)/60
	if colon {
		return fmt.Sprintf("%s%02d:%02d", sign, h, m)
	}
	return fmt.Sprintf("%s%02d%02d", sign, h, m)
}

var (
	locCache   sync.Map // string → *time.Location
	zoneByName map[string]TimeZone
	zoneOnce   sync.Once
)

func loadLocation(id string) (*time.Location, error) {
	if v, ok := locCache.Load(id); ok {
		return v.(*time.Location), nil
	}
	loc, err := time.LoadLocation(id)
	if err != nil {
		return nil, err
	}
	locCache.Store(id, loc)
	return loc, nil
}

// AllTimeZones は ActiveSupport::TimeZone.all（オフセット → 名前順）を返す。
func AllTimeZones() []TimeZone {
	return append([]TimeZone(nil), timeZones...)
}

// TimeZoneOptions は time_zone_select の選択肢 [[表示文字列, 値], ...] を返す（例: ["(GMT+09:00) Tokyo", "Tokyo"]）。
func TimeZoneOptions() [][2]string {
	out := make([][2]string, len(timeZones))
	for i, z := range timeZones {
		out[i] = [2]string{z.String(), z.Name}
	}
	return out
}

// FindTimeZone は ActiveSupport::TimeZone[name] 相当。ActiveSupport の名前、または IANA 名を受け付ける。
// IANA 名の場合、Name/Identifier ともにその名前で、UTCOffset は現在の標準オフセットの近似となる。
func FindTimeZone(name string) (TimeZone, bool) {
	zoneOnce.Do(func() {
		zoneByName = make(map[string]TimeZone, len(timeZones))
		for _, z := range timeZones {
			zoneByName[z.Name] = z
		}
	})
	if name == "" {
		return TimeZone{}, false
	}
	if z, ok := zoneByName[name]; ok {
		return z, true
	}
	if !strings.Contains(name, "/") && name != "UTC" {
		return TimeZone{}, false
	}
	loc, err := loadLocation(name)
	if err != nil {
		return TimeZone{}, false
	}
	return TimeZone{Name: name, Identifier: name, UTCOffset: standardOffset(loc)}, true
}

// UserLocation は pref.time_zone の値から *time.Location を返す（空・不明なら nil = タイムゾーン未設定）。
func UserLocation(prefTimeZone string) *time.Location {
	z, ok := FindTimeZone(prefTimeZone)
	if !ok {
		return nil
	}
	loc, err := z.Location()
	if err != nil {
		return nil
	}
	return loc
}

// standardOffset は現在年の 1 月・7 月のうち夏時間でない方のオフセットを返す。
func standardOffset(loc *time.Location) int {
	y := clock.Now().Year()
	jan := time.Date(y, 1, 1, 0, 0, 0, 0, loc)
	jul := time.Date(y, 7, 1, 0, 0, 0, 0, loc)
	_, oj := jan.Zone()
	_, ol := jul.Zone()
	if jul.IsDST() && !jan.IsDST() {
		return oj
	}
	if jan.IsDST() && !jul.IsDST() {
		return ol
	}
	if oj < ol {
		return oj
	}
	return ol
}
