
double transtime;
int iterationCount = 0;
int find_cnt = 0;

Action()
{

int HTTP_rc=0;

lr_continue_on_error( 1 );

	web_set_timeout(RECEIVE,"500");
	web_set_timeout(STEP,"500");
	web_set_sockets_option("SSL_VERSION", "TLS1.3");
	web_cleanup_cookies();
	web_cache_cleanup();
	web_add_cookie("session_dark=true; DOMAIN=.example.test; PATH=/");
	lr_start_transaction("ACME_RCI");

	web_add_header("Content-Type", "application/xml");

web_reg_save_param("ConvID", "LB=conversationId>" , "RB=</", "NotFound=warning", LAST);
	web_reg_find("Savecount=SuccessCount","Text=Success","Search=ALL", LAST);

	web_custom_request("web_custom_request",
		"URL=https://svc.example.test:1443/Services/ReserveContractIdentifier.jws",
		"Method=POST",
		"TargetFrame=",
		"Resource=0",
		"Referer=",
		"Mode=HTTP",
		"Body=<Envelope xmlns=\"http://schemas.xmlsoap.org/soap/envelope/\"> \r\n"
		"  <Body> \r\n"
		"    <ReserveContractIdentifierRequest/> \r\n"
		"  </Body> \r\n"
		"</Envelope>",
		LAST);

	HTTP_rc = web_get_int_property(HTTP_INFO_RETURN_CODE);

	if(atoi(lr_eval_string("{SuccessCount}"))>0)
	{
			lr_end_transaction("ACME_RCI", LR_PASS);

		}else{
		lr_end_transaction("ACME_RCI", LR_FAIL);

		lr_error_message("ACME_RCI failed Conv_id: %s",lr_eval_string("{ConvID}"));

		lr_exit(LR_EXIT_MAIN_ITERATION_AND_CONTINUE, LR_FAIL);

	}

	return 0;
}
