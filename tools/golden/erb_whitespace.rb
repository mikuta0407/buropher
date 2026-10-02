# frozen_string_literal: true

# internal/view の ERB 互換性テスト用フィクスチャを生成する。
# <dir>/*.erb を Redmine 6.1.2（Rails 7.2, Erubi trim モード）の ERB ハンドラで描画し、
# 結果を同名の *.out に書き出す。Go 側は同名の *.tmpl を描画して *.out と比較する。
#
# 使い方（DB へは書き込まない）:
#   cd _reference/redmine-migrated
#   SECRET_KEY_BASE=x RAILS_ENV=production bin/rails runner \
#     /path/to/buropher/tools/golden/erb_whitespace.rb /path/to/buropher/internal/view/testdata/erb
#
# ローカル変数は <dir>/data.json から与える（キーが html_safe のものは html_safe 文字列にする）。
# CSRF トークンは "TOKEN"、form の name 属性の乱数部は "abcd1234" に固定する。

require 'json'
require 'securerandom'

dir = ARGV[0] or abort "usage: rails runner erb_whitespace.rb <dir>"
data = JSON.parse(File.read(File.join(dir, 'data.json')))
locals = data.to_h { |k, v| [k.to_sym, k == 'html_safe' ? v.html_safe : v] }

def SecureRandom.hex(*) = "abcd1234"

Dir[File.join(dir, '*.erb')].sort.each do |file|
  c = ApplicationController.new
  c.request = ActionDispatch::TestRequest.create
  c.response = ActionDispatch::TestResponse.new
  v = c.view_context
  def v.protect_against_forgery? = true
  def v.form_authenticity_token(*, **) = "TOKEN"
  src = File.read(file)
  tmpl = ActionView::Template.new(src, file, ActionView::Template::Handlers::ERB.new, locals: locals.keys, format: :html)
  out = tmpl.render(v, locals) { |*name| v._layout_for(*name) }
  File.write(file.sub(/\.erb\z/, '.out'), out)
  puts "#{File.basename(file)}: #{out.bytesize} bytes"
end
