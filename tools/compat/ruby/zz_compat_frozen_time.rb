# frozen_string_literal: true
#
# buropher 互換テスト用イニシャライザ（redmine-ref.sh が redmine-fixtures にのみ配置する）。
# COMPAT_FROZEN_TIME が設定されていればプロセス全体の Time.now / Date.today を固定し、
# 相対時刻表示・期日計算・last_login_on などを決定的にする。
if ENV['COMPAT_FROZEN_TIME'].present?
  require 'active_support/testing/time_helpers'
  Rails.application.config.after_initialize do
    t = Time.zone.parse(ENV['COMPAT_FROZEN_TIME'])
    Object.new.extend(ActiveSupport::Testing::TimeHelpers).travel_to(t)
    Rails.logger.info("[compat] time frozen at #{t.utc}")
  end
end
