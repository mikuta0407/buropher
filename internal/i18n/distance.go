package i18n

import (
	"github.com/mikuta0407/buropher/internal/clock"
	"math"
	"time"
)

const (
	minutesInYear             = 525600
	minutesInQuarterYear      = 131400
	minutesInThreeQuarterYear = 394200
)

// DistanceOfTimeInWords は ActionView の distance_of_time_in_words（Rails 7.2）の移植。
// includeSeconds は include_seconds: オプション。
func (b *Bundle) DistanceOfTimeInWords(locale string, from, to time.Time, includeSeconds bool) string {
	if from.After(to) {
		from, to = to, from
	}
	diff := to.Sub(from).Seconds()
	minutes := rubyRound(diff / 60.0)
	seconds := rubyRound(diff)
	t := func(key string, count any) string {
		var vars Vars
		if count != nil {
			vars = Vars{"count": count}
		}
		return b.T(locale, "datetime.distance_in_words."+key, vars)
	}
	switch {
	case minutes <= 1:
		if !includeSeconds {
			if minutes == 0 {
				return t("less_than_x_minutes", int64(1))
			}
			return t("x_minutes", minutes)
		}
		switch {
		case seconds <= 4:
			return t("less_than_x_seconds", int64(5))
		case seconds <= 9:
			return t("less_than_x_seconds", int64(10))
		case seconds <= 19:
			return t("less_than_x_seconds", int64(20))
		case seconds <= 39:
			return t("half_a_minute", nil)
		case seconds <= 59:
			return t("less_than_x_minutes", int64(1))
		default:
			return t("x_minutes", int64(1))
		}
	case minutes < 45:
		return t("x_minutes", minutes)
	case minutes < 90:
		return t("about_x_hours", int64(1))
	case minutes < 1440:
		return t("about_x_hours", rubyRound(float64(minutes)/60.0))
	case minutes < 2520:
		return t("x_days", int64(1))
	case minutes < 43200:
		return t("x_days", rubyRound(float64(minutes)/1440.0))
	case minutes < 86400:
		return t("about_x_months", rubyRound(float64(minutes)/43200.0))
	case minutes < 525600:
		return t("x_months", rubyRound(float64(minutes)/43200.0))
	}
	fromYear := int64(from.Year())
	if from.Month() >= 3 {
		fromYear++
	}
	toYear := int64(to.Year())
	if to.Month() < 3 {
		toYear--
	}
	var leapYears int64
	if fromYear <= toYear {
		fy := fromYear - 1
		leapYears = (floorDiv(toYear, 4) - floorDiv(toYear, 100) + floorDiv(toYear, 400)) -
			(floorDiv(fy, 4) - floorDiv(fy, 100) + floorDiv(fy, 400))
	}
	withOffset := minutes - leapYears*1440
	remainder := floorMod(withOffset, minutesInYear)
	years := floorDiv(withOffset, minutesInYear)
	switch {
	case remainder < minutesInQuarterYear:
		return t("about_x_years", years)
	case remainder < minutesInThreeQuarterYear:
		return t("over_x_years", years)
	default:
		return t("almost_x_years", years+1)
	}
}

// TimeAgoInWords は time_ago_in_words（現在時刻との差）。
func (b *Bundle) TimeAgoInWords(locale string, from time.Time, includeSeconds bool) string {
	return b.DistanceOfTimeInWords(locale, from, clock.Now(), includeSeconds)
}

// DistanceOfDateInWords は Redmine が上書きした distance_of_date_in_words（config/initializers/10-patches.rb）。
func (b *Bundle) DistanceOfDateInWords(locale string, from, to time.Time) string {
	days := civilDays(to) - civilDays(from)
	if days < 0 {
		days = -days
	}
	t := func(key string, count int64) string {
		return b.T(locale, "datetime.distance_in_words."+key, Vars{"count": count})
	}
	switch {
	case days <= 60:
		return t("x_days", days)
	case days <= 720:
		// (Rational / 30).round（0.5 は切り上げ）
		return t("about_x_months", int64(math.Floor(float64(days)/30.0+0.5)))
	default:
		return t("over_x_years", days/365)
	}
}

// civilDays は日付部分のみの通日（タイムゾーンはその時刻のもの）。
func civilDays(t time.Time) int64 {
	y, m, d := t.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, time.UTC).Unix() / 86400
}
