int Poll_0_RequestCB()
{
	//enter your implementation for RequestCB() here

	return WEB_ASYNC_CB_RC_OK;
}

int Poll_0_ResponseBodyBufferCB(
	const char *	aLastBufferStr,
	int				aLastBufferLen,
	const char *	aAccumulatedStr,
	int				aAccumulatedLen,
	int				aHttpStatusCode)
{
	//enter your implementation for ResponseBodyBufferCB() here

	return WEB_ASYNC_CB_RC_OK;
}

int Poll_0_ResponseCB(
	const char *	aResponseHeadersStr,
	int				aResponseHeadersLen,
	const char *	aResponseBodyStr,
	int				aResponseBodyLen,
	int				aHttpStatusCode)
{
	lr_save_string("yes", "PollSeen");

	return WEB_ASYNC_CB_RC_OK;
}
