Action()
{
	web_reg_async_attributes("ID=Poll_0",
		"Pattern=Poll",
		"URL=http://parity.test/api/status",
		"PollIntervalMs=200",
		"RequestCB=Poll_0_RequestCB",
		"ResponseBodyBufferCB=Poll_0_ResponseBodyBufferCB",
		"ResponseCB=Poll_0_ResponseCB",
		LAST);

	web_reg_save_param_json(
		"ParamName=JobStatus",
		"QueryString=$.status",
		"NotFound=warning",
		SEARCH_FILTERS,
		"Scope=BODY",
		LAST);

	web_url("status",
		"URL=http://parity.test/api/status",
		"Resource=0",
		"RecContentType=application/json",
		"Mode=HTTP",
		LAST);

	web_sync("ParamValueEquals=JobStatus=done",
		"Timeout=10",
		LAST);

	web_stop_async("ID=Poll_0",
		LAST);

	web_url("result",
		"URL=http://parity.test/app/result?job={JobStatus}&seen={PollSeen}",
		"Resource=0",
		"RecContentType=text/html",
		"Mode=HTTP",
		LAST);

	return 0;
}
