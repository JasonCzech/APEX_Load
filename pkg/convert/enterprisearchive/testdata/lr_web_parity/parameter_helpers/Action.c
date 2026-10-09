Action()
{
	web_save_timestamp_param("TS", LAST);

	lr_save_string("abcdef", "Token");
	web_save_param_length("Token", LAST);

	lr_save_string("07/03/2026", "Day");
	web_convert_date_param("Day",
		"SourceFormat=%d/%m/%Y",
		"TargetFormat=%Y-%m-%d",
		LAST);

	web_url("cache_buster",
		"URL=http://parity.test/app/data?_={TS}&len={Token_Length}&d={Day}",
		"Resource=0",
		LAST);

	return 0;
}
