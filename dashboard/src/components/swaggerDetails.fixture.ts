export const referenceFixture = `openapi: 3.0.3
info:
  title: Reference test
  version: 1
components:
  securitySchemes:
    bearerAuth:
      type: http
      scheme: bearer
    collector:
      type: apiKey
      in: header
      name: X-Collector-Token
  schemas:
    Error:
      type: object
      required: [error]
      properties:
        error: {type: string}
    Node:
      type: object
      properties:
        child: {$ref: '#/components/schemas/Node'}
  examples:
    falseValue: {value: false, summary: Disabled}
security:
  - bearerAuth: []
paths:
  /api/v1/widgets/{id}:
    post:
      operationId: widgets
      parameters:
        - name: id
          in: path
          required: true
          schema: {type: string}
        - name: revision
          in: query
          required: true
          schema: {type: integer}
        - name: If-Match
          in: header
          required: true
          description: Exact revision
          schema: {type: string}
      requestBody:
        required: true
        content:
          application/json:
            schema:
              type: object
              properties:
                nested:
                  type: array
                  items: {$ref: '#/components/schemas/Node'}
            example: {name: "O'Brien", active: false}
      responses:
        '201':
          description: Created
          content:
            application/json:
              example: {id: widget-1}
              schema: {type: object}
        '400':
          description: Invalid request
          content:
            application/json:
              schema: {$ref: '#/components/schemas/Error'}
`
