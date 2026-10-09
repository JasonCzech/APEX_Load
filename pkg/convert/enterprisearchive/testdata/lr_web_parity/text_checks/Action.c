Action()
{
	web_reg_find("Text=X-Status: ok", "Search=Headers",
		LAST);

	web_url("headers_ok",
		"URL=http://parity.test/check/headers_ok",
		"Resource=0",
		LAST);

	web_reg_find("Text=X-Status: ok", "Search=Headers",
		LAST);

	web_url("headers_broken",
		"URL=http://parity.test/check/headers_broken",
		"Resource=0",
		LAST);

	web_reg_find("TextPfx=<title>", "TextSfx=</title>",
		LAST);

	web_url("prefix_suffix_ok",
		"URL=http://parity.test/check/prefix_suffix_ok",
		"Resource=0",
		LAST);

	web_reg_find("TextPfx=<title>", "TextSfx=</title>",
		LAST);

	web_url("prefix_suffix_broken",
		"URL=http://parity.test/check/prefix_suffix_broken",
		"Resource=0",
		LAST);

	web_reg_find("Text/DIG=Order ##",
		LAST);

	web_url("digits_ok",
		"URL=http://parity.test/check/digits_ok",
		"Resource=0",
		LAST);

	web_reg_find("Text/DIG=Order ##",
		LAST);

	web_url("digits_broken",
		"URL=http://parity.test/check/digits_broken",
		"Resource=0",
		LAST);

	web_reg_find("Text/ALNUMIC=id=^^^;",
		LAST);

	web_url("alphanumerics_ok",
		"URL=http://parity.test/check/alphanumerics_ok",
		"Resource=0",
		LAST);

	web_reg_find("Text/ALNUMIC=id=^^^;",
		LAST);

	web_url("alphanumerics_broken",
		"URL=http://parity.test/check/alphanumerics_broken",
		"Resource=0",
		LAST);

	web_reg_find("Text/BIN=ok\\x01",
		LAST);

	web_url("binary_ok",
		"URL=http://parity.test/check/binary_ok",
		"Resource=0",
		LAST);

	web_reg_find("Text/BIN=ok\\x01",
		LAST);

	web_url("binary_broken",
		"URL=http://parity.test/check/binary_broken",
		"Resource=0",
		LAST);

	web_reg_find("Text/REGEXP=status=(ok|done)",
		LAST);

	web_url("regexp_ok",
		"URL=http://parity.test/check/regexp_ok",
		"Resource=0",
		LAST);

	web_reg_find("Text/REGEXP=status=(ok|done)",
		LAST);

	web_url("regexp_broken",
		"URL=http://parity.test/check/regexp_broken",
		"Resource=0",
		LAST);

	web_reg_find("Text/IC=WELCOME",
		LAST);

	web_url("ignore_case_ok",
		"URL=http://parity.test/check/ignore_case_ok",
		"Resource=0",
		LAST);

	web_reg_find("Text/IC=WELCOME",
		LAST);

	web_url("ignore_case_broken",
		"URL=http://parity.test/check/ignore_case_broken",
		"Resource=0",
		LAST);

	web_reg_find("Text=Error", "Fail=Found",
		LAST);

	web_url("fail_when_found_ok",
		"URL=http://parity.test/check/fail_when_found_ok",
		"Resource=0",
		LAST);

	web_reg_find("Text=Error", "Fail=Found",
		LAST);

	web_url("fail_when_found_broken",
		"URL=http://parity.test/check/fail_when_found_broken",
		"Resource=0",
		LAST);

	return 0;
}
