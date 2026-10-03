// SPDX-License-Identifier: GPL-2.0-or-later
// Copyright (C) 2026 mikuta0407 and Buropher contributors

// Package view は Redmine の ERB ビューを移植した Go テンプレートの描画エンジン。
//
// # 配置と名前
//
// テンプレートは fs.FS（本番は web.Templates() の embed、開発時は os.DirFS + Options.Reload）から
// 読み込み、Redmine の app/views と同じツリー・同じファイル名で置く（拡張子 .erb → .tmpl）:
//
//	issues/show.html.tmpl         ビュー            （app/views/issues/show.html.erb）
//	issues/_attributes.html.tmpl  部分テンプレート  （app/views/issues/_attributes.html.erb）
//	layouts/base.html.tmpl        レイアウト        （app/views/layouts/base.html.erb）
//	issues/new.js.tmpl            XHR の JS 応答    （app/views/issues/new.js.erb）
//	mailer/issue_add.text.tmpl    メール本文        （app/views/mailer/issue_add.text.erb）
//
// テンプレート名は .tmpl を除いたパス（"issues/show.html"）。ファイル内の {{define}} も同じ名前空間に
// 入るため、名前は「ファイル名（形式なし）#ブロック名」（例 "issues/show#sidebar"）とする。重複はエラー。
//
// # エスケープ（ERB と同一）
//
// html/template は使わない。html/template の文脈依存エスケープは ERB と異なるバイト列を出力するため
// （" → &#34;、+ → &#43;、HTML コメントの削除、<script> 内の値の JSON 文字列化、URL の #ZgotmplZ 化など）、
// Redmine と同一の HTML を出せない。代わりに text/template で解析し、出力を伴うすべてのアクションの
// パイプライン末尾にエスケープ関数を自動で追加する。規則は ERB の <%= %> と同じ:
//
//   - template.HTML（= Ruby の html_safe な文字列）はそのまま出力する
//   - それ以外は Ruby の to_s 相当で文字列化し、ERB::Util.html_escape で & " ' < > を
//     &amp; &quot; &#39; &lt; &gt; に変換する（nil は空文字列、1.0 は "1.0"）
//   - 文脈（属性値・<script>・<style>・<textarea>）によらず同じ規則（ERB も文脈を見ない）
//
// したがって安全性も ERB と同じで、ヘルパーは「安全な HTML なら template.HTML を返す」こと。
// 生の HTML を出す場合は {{raw .X}}（Ruby の raw / html_safe）。
// .js.tmpl も同じ規則（Rails の .js.erb も HTML エスケープする）。JS 文字列へ埋め込む値は
// {{j .X}}（escape_javascript）を使う。j は入力が安全なら安全な値を、そうでなければ文字列を返すため、
// Rails と同じく安全でない値は j の後にさらに HTML エスケープされる。
// .text.tmpl（text/plain）はエスケープしない（Rails の escape_ignore_list と同じ）。
//
// # 空白・改行（ERB の trim モードと同一）
//
// 解析前に ERBTrim で Erubi（Rails の ERB ハンドラ, trim: true）と同じ空白規則を適用するため、
// ERB を行単位でそのまま置き換えれば同じ出力になる。{{- ... -}} による調整は原則不要。
//
//	ERB                                   Go テンプレート
//	<% if @issue.closed? %>               {{if .Issue.Closed}}
//	<% else %> / <% elsif x %>            {{else}} / {{else if .X}}
//	<% end %>                             {{end}}
//	<% @issues.each do |issue| %>         {{range $issue := .Issues}}
//	<% x = 1 %>                           {{$x := 1}}
//	<%# コメント %>                       {{/* コメント */}}
//	<%= expr %>                           {{expr}}
//	<%= expr -%>                          {{expr -}}
//
// 規則:
//
//  1. 出力しないアクションが行に単独で置かれた場合（前は行頭からスペース・タブのみ、後は
//     [ \t]*\n）、行ごと（行頭の空白と行末の改行を含めて）消える。ERB の <% %> と同じ。
//     出力しないアクション = if / else / end / range / with / define / block / break / continue、
//     コメント、変数の宣言・代入、および「文」関数（下記）。
//  2. 右トリム " -}}" は ERB の -%> と同じく直後の [ \t]*\n だけを消す（Go 本来の、改行を含む
//     空白すべての削除ではない）。出力アクションにだけ使うこと。
//  3. 出力アクションの前後の空白はそのまま残る。1 行に複数のタグがある行は消えない（ERB と同じ）。
//  4. 左トリム "{{- " は Go 本来の意味（直前の空白・改行をすべて削除）のまま。ERB に対応物がないため使わない。
//
// 「文」関数（行に単独で置かれたら <% %> と同様に行ごと消える）: content_for（値を渡す場合）,
// provide, html_title（引数がある場合）, reset_cycle, end_form, end_tag。Options.StatementFuncs で追加できる。
// 規則 1 で消えた改行はアクションの内側に移すので、エラーメッセージの行番号は元ファイルと一致する。
//
// # ブロック付きヘルパー
//
// Go テンプレートはブロックを関数に渡せないため、ERB のブロックは「開始」と「終了」に分けて書く。
// 終了側は <% end %> と同じ位置に置く（行単独なら行ごと消える）:
//
//	<%= form_tag(path, :method => :get) do %>       {{form_tag .Path (hash "method" "get")}}
//	  ...                                            ...
//	<% end %>                                        {{end_form}}
//
//	<%= labelled_form_for @issue, :url => u do |f| %>   {{$f := labelled_form_for "issue" .Issue (hash "url" .URL)}}
//	                                                     {{$f.Open}}
//	  <%= f.text_field :subject, :size => 80 %>          {{$f.TextField "subject" (hash "size" 80)}}
//	<% end %>                                            {{end_form}}
//
//	<%= content_tag 'div', :class => 'x' do %>      {{content_tag_open "div" (hash "class" "x")}}
//	<% end %>                                        {{end_tag "div"}}
//	<%= field_set_tag l(:label) do %>               {{field_set_tag_open (l "label")}} ... {{end_tag "fieldset"}}
//
//	<% content_for :sidebar do %>                   {{define "issues/index#sidebar"}}
//	  ...                                              ...
//	<% end %>                                        {{end}}
//	                                                 {{content_for "sidebar" (capture "issues/index#sidebar")}}
//
// capture の第 2 引数を省略するとビューのデータ（assigns）が渡る。部分テンプレート内では . を渡すこと。
// form_for の multipart は自動判定できないので html: {multipart: true} を明示する。
//
// # 引数の書き方
//
// Ruby のハッシュ引数は {{hash "key" value ...}}（rails.Hash、挿入順を保持）で渡す。Rails の属性出力順は
// ハッシュの順序に従うため、属性を出すヘルパーには必ず hash を使う。シンボル値は {{sym "label_x"}}。
// 部分テンプレートの locals は {{dict "key" value ...}}（map[string]any）。配列は {{list a b}}。
//
// # 部分テンプレートとレイアウト
//
//	<%= render :partial => 'issues/attributes', :locals => {:issue => @issue} %>
//	  → {{partial "issues/attributes" (dict "issue" .Issue)}}
//	<%= render :partial => 'issue', :collection => @issues %>
//	  → {{render_collection "issues/issue" .Issues}}（各要素は issue / issue_counter / issue_iteration）
//	render :partial => 'x', :collection => c, :as => :y → {{render_collection_as "x" .C "y"}}
//
// 部分テンプレートの . は locals（map[string]any）。ビューのデータ（ERB のインスタンス変数）は {{assigns}}。
// スラッシュのない名前はコントローラ名のディレクトリ、次に application/ を探す。js 形式で見つからなければ
// html 形式を探す（Rails と同じ）。
//
// Engine.Render はビューを描画したあとレイアウト layouts/<name>.<format> を同じデータで描画する。
// レイアウトでは {{yield}}（本文）, {{yield "header_tags"}}, {{has_content_for "sidebar"}}（content_for?）を使う。
// html 形式の既定レイアウトは "base"、他の形式はレイアウトなし（RenderOptions.Layout で指定、NoLayout で無効）。
//
// # リクエストコンテキストと関数
//
// Context（ロケール、翻訳関数、CSRF トークン、コントローラ/アクション名、現在のユーザー・プロジェクト、
// flash など）は Render ごとに束縛された関数から参照する:
// current_user, current_project, controller_name, action_name, current_language, request_path,
// csrf_meta_tags, form_authenticity_token, body_css_classes, html_title, render_flash_messages, l, assigns, ctx。
// 他パッケージのヘルパー（Redmine の ApplicationHelper 等）は Options.Funcs（リクエスト非依存）または
// Options.RequestFuncs（*Render を受け取る。名前収集のためダミーの Render でも呼ばれるので panic しないこと）で登録する。
// i18n・アセットは依存を避けるため Context.T（Translator）/ Context.AssetPath（AssetPathFunc）で受け取る。
//
// ActionView ヘルパー（link_to, form_tag, select_tag, options_for_select, text_field_tag, javascript_tag,
// simple_format, truncate, cycle など）は internal/view/rails にあり、すべて登録済み。
//
// # サニティチェック
//
// TestWebTemplatesParse は web/templates の全テンプレートを（関数の存在検査なしで）解析する。
// 全ヘルパーを組み立てるパッケージでは Options.Funcs/RequestFuncs を揃えて New を呼ぶテストを置き、
// 未定義関数も検出すること。
package view
