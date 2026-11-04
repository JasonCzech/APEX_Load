import { createServer } from 'node:http'
import type { AddressInfo, Socket } from 'node:net'
import type { APIRequestContext } from '@playwright/test'
import { test, expect, idOf, json, login, runStatus } from './support/workflow'
import { createWorkspace, scopeQuery } from './support/workspace'

async function startMockLLMServer() {
  const sockets = new Set<Socket>()
  let requests = 0

  const server = createServer(async (req, res) => {
    requests++
    for await (const chunk of req) void chunk
    if (req.url !== '/v1/chat/completions') {
      res.statusCode = 404
      res.end('{"error":"not found"}')
      return
    }
    res.statusCode = 200
    res.setHeader('Content-Type', 'text/event-stream; charset=utf-8')
    res.setHeader('Cache-Control', 'no-cache')
    res.setHeader('Connection', 'keep-alive')
    res.setHeader('x-apexllm-queue-ms', '4.5')
    res.setHeader('x-apexllm-prefill-ms', '22.0')
    res.setHeader('x-apexllm-decode-ms', '64.0')

    await new Promise(resolve => setTimeout(resolve, 25))
    const tokens = ['Continuous', ' batching', ' separates', ' prefill', ' from', ' decode', ' phases', '.']
    for (let i = 0; i < tokens.length; i++) {
      if (i > 0) await new Promise(resolve => setTimeout(resolve, 8))
      const chunk = {
        id: `chatcmpl-e2e-${requests}`,
        object: 'chat.completion.chunk',
        model: 'gpt-4o-mini',
        choices: [
          {
            index: 0,
            delta: i === 0 ? { role: 'assistant', content: tokens[i] } : { content: tokens[i] },
            finish_reason: null,
          },
        ],
      }
      res.write(`data: ${JSON.stringify(chunk)}\n\n`)
    }

    const finalChunk = {
      id: `chatcmpl-e2e-${requests}`,
      object: 'chat.completion.chunk',
      model: 'gpt-4o-mini',
      choices: [{ index: 0, delta: {}, finish_reason: 'stop' }],
      usage: {
        prompt_tokens: 48,
        completion_tokens: 8,
        total_tokens: 56,
        prompt_tokens_details: { cached_tokens: 12 },
        completion_tokens_details: { reasoning_tokens: 2 },
      },
    }
    res.write(`data: ${JSON.stringify(finalChunk)}\n\n`)
    res.write('data: [DONE]\n\n')
    res.end()
  })

  server.on('connection', socket => {
    sockets.add(socket)
    socket.once('close', () => sockets.delete(socket))
  })

  await new Promise<void>(resolve => server.listen(0, '127.0.0.1', resolve))
  const port = (server.address() as AddressInfo).port

  return {
    baseUrl: `http://127.0.0.1:${port}`,
    requestCount: () => requests,
    async close() {
      for (const socket of sockets) socket.destroy()
      await new Promise<void>(resolve => server.close(() => resolve()))
    },
  }
}

async function waitForLLMArchiveReport(request: APIRequestContext, testId: string): Promise<Record<string, unknown>> {
  const deadline = Date.now() + 60_000
  while (Date.now() < deadline) {
    const response = await request.get(`/api/v1/tests/${testId}/archive/report`)
    if (response.ok()) {
      const payload = (await response.json()) as Record<string, unknown>
      const llm = (payload.llm ?? payload.llm_inference) as { total_requests?: number } | undefined
      if (llm && (llm.total_requests ?? 0) > 0) {
        return payload
      }
    }
    await new Promise(resolve => setTimeout(resolve, 500))
  }
  throw new Error(`Archive report with LLM metrics did not become ready for test ${testId}`)
}

test('streaming llm_request run verifies goal forms, LLM chart tab, report panel, and capacity sweep panel', async ({ page, trackRun }, testInfo) => {
  test.setTimeout(120_000)
  await page.addInitScript(() => {
    const mode = window.sessionStorage.getItem('e2eRunReportMode') || 'false'
    window.localStorage.setItem('apexload.featureFlags.runReport', mode)
  })

  const mockLLM = await startMockLLMServer()
  try {
    if (process.env.APEXLOAD_E2E_PROFILE === 'local-auth') await login(page)
    const request = page.context().request
    const workspace = await createWorkspace(request)

    const scriptId = idOf(
      await json(
        await request.post('/api/v1/scripts', {
          data: {
            project_id: workspace.projectId,
            script: {
              name: 'LLM Streaming Benchmark E2E',
              version: '1.0.0',
              protocol: 'http',
              config: {
                base_url: mockLLM.baseUrl,
                pacing: { type: 'fixed_interval', interval_ms: 200 },
              },
              actions: [
                {
                  name: 'chat_stream',
                  type: 'llm_request',
                  target: '/v1/chat/completions',
                  params: {
                    provider: 'openai_chat',
                    model: 'gpt-4o-mini',
                    stream: true,
                    prompt: 'Explain continuous batching and TTFT vs TPOT.',
                    max_tokens: 64,
                    temperature: 0,
                    golden_prompt: true,
                  },
                },
              ],
            },
          },
        }),
      ),
    )

    const testId = idOf(
      await json(
        await request.post('/api/v1/tests', {
          data: {
            project_id: workspace.projectId,
            application_id: workspace.applicationId,
            release_id: workspace.releaseId,
            name: 'LLM Streaming Run E2E',
            duration: '4s',
            scenario_mode: 'goal_oriented',
            goal_config: {
              target_average_tps: 10,
              max_llm_p95_ttft_ms: 400,
              max_llm_p95_tpot_ms: 50,
              min_llm_output_tokens_per_sec: 10,
              min_llm_goodput_pct: 90,
              max_llm_throttle_rate_pct: 5,
              workload_shape_config: {
                capacity_sweep: {
                  mode: 'concurrency',
                  levels: [1, 2],
                  warmup_duration: '0s',
                  measure_duration: '2s',
                  slo_ttft_ms: 400,
                  slo_tpot_ms: 50,
                },
              },
            },
            groups: [{ name: 'llm-users', script_id: scriptId, vusers: 2, ramp_up: '0s' }],
          },
        }),
      ),
    )
    trackRun(testId)

    const pageErrors: Error[] = []
    const onPageError = (err: Error) => pageErrors.push(err)
    page.on('pageerror', onPageError)
    try {
      // 1. Verify Goal Forms in the browser before starting the run
      await page.goto(`/#/command-center/${testId}?${scopeQuery(workspace)}`)
      await page.getByRole('button', { name: 'Create revised queued copy', exact: true }).click()
      const dialog = page.getByRole('dialog', { name: 'Create revised queued run', exact: true })
      await expect(dialog).toBeVisible()
      const ttftInput = dialog.locator('label', { hasText: 'Max LLM P95 TTFT (ms)' }).locator('input')
      const tpotInput = dialog.locator('label', { hasText: 'Max LLM P95 TPOT (ms)' }).locator('input')
      const tokInput = dialog.locator('label', { hasText: 'Min LLM Output (tok/s)' }).locator('input')
      const goodputInput = dialog.locator('label', { hasText: 'Min LLM Goodput (%)' }).locator('input')
      const throttleInput = dialog.locator('label', { hasText: 'Max LLM 429 Throttle Rate (%)' }).locator('input')
      await expect(ttftInput).toHaveValue('400')
      await expect(tpotInput).toHaveValue('50')
      await expect(tokInput).toHaveValue('10')
      await expect(goodputInput).toHaveValue('90')
      await expect(throttleInput).toHaveValue('5')
      await ttftInput.fill('350')
      await expect(ttftInput).toHaveValue('350')
      await ttftInput.fill('400')
      await dialog.getByRole('button', { name: 'Cancel', exact: true }).click()
      await expect(dialog).toBeHidden()

      // 2. Start the test and wait for completion
      await json(await request.post(`/api/v1/tests/${testId}/start`))
      await expect.poll(() => mockLLM.requestCount(), { timeout: 30_000 }).toBeGreaterThan(0)
      await expect.poll(() => runStatus(request, testId), { timeout: 45_000 }).toBe('COMPLETE')

      // 3. Verify the LLM Chart Tab in the browser on Command Center Graph Wall
      const graphWallBtn = page.getByRole('button', { name: 'Graph Wall', exact: true })
      await expect(graphWallBtn).toBeVisible({ timeout: 20_000 })
      await graphWallBtn.click()
      const graphTabs = page.getByRole('tablist', { name: 'Graph groups' })
      await expect(graphTabs).toBeVisible({ timeout: 20_000 })
      const llmTab = graphTabs.getByRole('tab', { name: /^LLM/ })
      await expect(llmTab).toBeVisible({ timeout: 20_000 })
      await expect(async () => {
        await llmTab.click()
        await expect(llmTab).toHaveAttribute('aria-selected', 'true', { timeout: 1_000 })
      }).toPass({ timeout: 10_000 })
      const llmKpis = page.getByRole('region', { name: 'LLM live KPIs' })
      await expect(llmKpis).toBeVisible({ timeout: 15_000 })
      await expect(llmKpis).toContainText('Output tok/s:')
      await expect(llmKpis).toContainText('TTFT p95:')
      await expect(llmKpis).toContainText('TPOT p95:')
      await expect(llmKpis).toContainText('Goodput %:')

      const reportPayload = await waitForLLMArchiveReport(request, testId)
      const llmReport = reportPayload.llm as {
        total_requests: number
        streamed_requests: number
        successful_requests: number
        prompt_tokens: number
        completion_tokens: number
        cached_tokens: number
        reasoning_tokens: number
      }
      expect(llmReport.total_requests).toBeGreaterThan(0)
      expect(llmReport.successful_requests).toBeGreaterThan(0)
      expect(llmReport.streamed_requests).toBeGreaterThanOrEqual(llmReport.successful_requests)
      expect(llmReport.prompt_tokens).toBeGreaterThanOrEqual(llmReport.successful_requests * 48)
      expect(llmReport.completion_tokens).toBeGreaterThanOrEqual(llmReport.successful_requests * 8)
      expect(llmReport.cached_tokens).toBe(llmReport.successful_requests * 12)
      expect(llmReport.reasoning_tokens).toBe(llmReport.successful_requests * 2)

      const decompResponse = await request.get(`/api/v1/tests/${testId}/archive/llm-latency-decomposition`)
      expect(decompResponse.ok()).toBeTruthy()
      const decomp = (await decompResponse.json()) as {
        source: string
        server_telemetry_samples: number
        server_avg_queue_ms: number
        server_avg_prefill_ms: number
        server_avg_decode_ms: number
      }
      expect(decomp.source).toBe('response_headers')
      expect(decomp.server_telemetry_samples).toBeGreaterThan(0)
      expect(decomp.server_avg_queue_ms).toBeCloseTo(4.5, 1)
      expect(decomp.server_avg_prefill_ms).toBeCloseTo(22.0, 1)
      expect(decomp.server_avg_decode_ms).toBeCloseTo(64.0, 1)

      const sweepResponse = await request.get(`/api/v1/tests/${testId}/archive/capacity-sweep`)
      expect(sweepResponse.ok()).toBeTruthy()

      // 4. Verify the Report Panel (RunReportLLMPanel) and Capacity Sweep Panel (CapacitySweepPanel) in the browser
      await page.evaluate(() => {
        window.sessionStorage.setItem('e2eRunReportMode', 'true')
        window.localStorage.setItem('apexload.featureFlags.runReport', 'true')
      })
      await page.goto(`/#/report/${testId}?${scopeQuery(workspace)}`)
      await page.reload()
      const llmDiagnostics = page.locator('details.llm-diagnostics-panel')
      await expect(llmDiagnostics).toBeVisible({ timeout: 30_000 })
      await expect(llmDiagnostics.locator('summary')).toHaveText('LLM inference diagnostics')

      const summaryTable = llmDiagnostics.getByRole('table', { name: 'LLM inference summary', exact: true })
      await expect(summaryTable).toBeVisible()
      const summaryRow = summaryTable.locator('tbody tr').first()
      await expect(summaryRow.locator('td').nth(0)).toHaveText(String(llmReport.total_requests))
      await expect(summaryRow.locator('td').nth(1)).toHaveText(String(llmReport.streamed_requests))
      await expect(summaryRow.locator('td').nth(7)).toHaveText(String(llmReport.prompt_tokens))
      await expect(summaryRow.locator('td').nth(8)).toHaveText(String(llmReport.completion_tokens))
      await expect(summaryRow.locator('td').nth(9)).toHaveText(String(llmReport.reasoning_tokens))
      await expect(summaryRow.locator('td').nth(10)).toHaveText(String(llmReport.cached_tokens))

      const modelBreakdown = llmDiagnostics.getByRole('region', { name: 'Breakdown by model', exact: true })
      await expect(modelBreakdown).toBeVisible()
      await expect(modelBreakdown).toContainText('gpt-4o-mini')

      const bandBreakdown = llmDiagnostics.getByRole('region', { name: 'Latency by prompt length band', exact: true })
      await expect(bandBreakdown).toBeVisible()

      const goldenVariance = llmDiagnostics.getByRole('region', { name: 'Golden prompt output variance', exact: true })
      await expect(goldenVariance).toBeVisible()
      await expect(goldenVariance).toContainText('chat_stream')

      const sweepSection = llmDiagnostics.getByRole('region', { name: 'Capacity sweep analysis', exact: true })
      await expect(sweepSection).toBeVisible()
      await expect(sweepSection.getByRole('img', { name: 'Capacity sweep latency vs throughput chart', exact: true })).toBeVisible()
      const sweepLevelsRegion = sweepSection.getByRole('region', { name: 'Capacity sweep load levels', exact: true })
      await expect(sweepLevelsRegion).toBeVisible()
      await expect(sweepLevelsRegion).toContainText('Level 1 (1)')

      const latencyDecomp = llmDiagnostics.getByRole('region', { name: 'LLM latency decomposition', exact: true })
      await expect(latencyDecomp).toBeVisible()
      await expect(latencyDecomp).toContainText('Decomposition source: response_headers')
      await expect(latencyDecomp).toContainText('4.5')
      await expect(latencyDecomp).toContainText('22.0')
      await expect(latencyDecomp).toContainText('64.0')

      await llmDiagnostics.scrollIntoViewIfNeeded()
      await page.screenshot({ path: testInfo.outputPath('llm-performance-report.png') })
      expect(pageErrors).toEqual([])
    } finally {
      page.off('pageerror', onPageError)
    }
  } finally {
    await mockLLM.close()
  }
})
