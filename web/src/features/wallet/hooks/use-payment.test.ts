/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
import { describe, expect, test } from 'vitest'

import { PAYMENT_TYPES } from '../constants'
import { requestPaymentAmount } from './use-payment'

describe('payment amount routing', () => {
  test('uses the dedicated Waffo amount calculator', async () => {
    const calls: string[] = []
    const amount = await requestPaymentAmount(120, PAYMENT_TYPES.WAFFO, {
      regular: async () => {
        calls.push('regular')
        return { success: true, data: '1' }
      },
      stripe: async () => {
        calls.push('stripe')
        return { success: true, data: '2' }
      },
      waffo: async (request) => {
        calls.push(`waffo:${request.amount}`)
        return { success: true, data: '18.75' }
      },
      waffoPancake: async () => {
        calls.push('pancake')
        return { success: true, data: '4' }
      },
      zafuPay: async () => {
        calls.push('zafuPay')
        return { success: true, data: '5' }
      },
    })

    expect(amount).toBe(18.75)
    expect(calls).toEqual(['waffo:120'])
  })

  test('throws the backend reason when the amount cannot be calculated', async () => {
    await expect(
      requestPaymentAmount(1, PAYMENT_TYPES.ZAFU_PAY, {
        regular: async () => ({ success: true, data: '1' }),
        stripe: async () => ({ success: true, data: '2' }),
        waffo: async () => ({ success: true, data: '3' }),
        waffoPancake: async () => ({ success: true, data: '4' }),
        zafuPay: async () => ({
          success: false,
          message: 'error',
          data: '充值金额过低，最低支付金额为 0.01 元',
        }),
      })
    ).rejects.toThrow('充值金额过低，最低支付金额为 0.01 元')
  })
})
