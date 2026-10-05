$("#journal-2-notes").hide();

if ($("form#journal-2-form").length > 0) {
  // journal edit form already loaded
  $("#journal-2-form").show();
} else {
  $("#journal-2-notes").after('<form id=\"journal-2-form\" action=\"/journals/2\" accept-charset=\"UTF-8\" data-remote=\"true\" name=\"journal-2-form-b2eb1555\" method=\"post\"><input type=\"hidden\" name=\"_method\" value=\"put\" autocomplete=\"off\" /><input type=\"hidden\" name=\"authenticity_token\" value=\"Nyga3iIdO-094Pe0zoKe-ZtCR6QQ6_caX2-z3OiaJvqtR7WjoSfeVqWGTBk5nVZismuEaNpBGGj1Us7_9g68aA\" autocomplete=\"off\" />\n    <label class=\"hidden-for-sighted\" for=\"journal_2_notes\">Notes<\/label>\n    <textarea name=\"journal[notes]\" id=\"journal_2_notes\" class=\"wiki-edit\" rows=\"10\" data-auto-complete=\"true\" data-controller=\"list-autofill selection-indent table-paste\" data-action=\"beforeinput-&gt;list-autofill#handleBeforeInput keydown.tab-&gt;selection-indent#run keydown.shift+tab-&gt;selection-indent#run paste-&gt;table-paste#handlePaste\" data-list-autofill-text-formatting-param=\"common_mark\" data-selection-indent-text-formatting-param=\"common_mark\" data-table-paste-text-formatting-param=\"common_mark\">\nSome notes with Redmine links: #2, r2.<\/textarea>\n      <input type=\"hidden\" name=\"journal[private_notes]\" id=\"journal_private_notes\" value=\"0\" autocomplete=\"off\" />\n      <input type=\"checkbox\" name=\"journal[private_notes]\" id=\"journal_2_private_notes\" value=\"1\" />\n      <label for=\"journal_2_private_notes\">Private notes<\/label>\n    \n    <p><input type=\"submit\" name=\"commit\" value=\"Save\" data-disable-with=\"Save\" />\n    <a onclick=\"\$(&#39;#journal-2-form&#39;).remove(); \$(&#39;#journal-2-notes&#39;).show(); return false;\" href=\"#\">Cancel<\/a><\/p>\n<\/form><script>\n//<![CDATA[\nvar wikiToolbar = new jsToolBar(document.getElementById(\'journal_2_notes\')); wikiToolbar.setHelpLink(\'/help/wiki_syntax\'); wikiToolbar.setPreviewUrl(\'/issues/preview?issue_id=1&project_id=ecookbook\'); wikiToolbar.draw();\n//]]>\n<\/script>\n');
}

// Focus on the textarea
(() => {
  const textarea = $("#journal-2-form .wiki-edit");
  if (textarea.length > 0) {
    textarea.focus();
    const textareaLength = textarea.val().length;
    textarea.get(0).setSelectionRange(textareaLength, textareaLength);
  }
})();
