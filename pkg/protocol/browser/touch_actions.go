package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/mxschmitt/playwright-go"
)

func (b *Browser) dispatchTap(ctx context.Context, action *protocol.Action, selector string) error {
	if b.shouldUseSyntheticTap() {
		return b.runTouchScript(ctx, action, selector, syntheticTapOperation(), "browser tap")
	}
	timeout := b.actionTimeout(action)
	var targetLocator playwright.Locator
	if locator, ok := b.actionLocator(action, selector); ok {
		if err := b.waitLocatorVisible(ctx, locator, timeout); err != nil {
			return err
		}
		targetLocator = locator
	} else {
		if err := b.waitVisibleWithTimeout(ctx, selector, timeout); err != nil {
			return err
		}
		page := b.currentPage()
		if page == nil {
			return fmt.Errorf("browser tap failed: no active page")
		}
		targetLocator = firstMatch(page, selector)
	}
	tapTimeout := timeout
	if b.engine == "webkit" && tapTimeout > 750*time.Millisecond {
		tapTimeout = 750 * time.Millisecond
	}
	err := targetLocator.Tap(playwright.LocatorTapOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, tapTimeout)),
	})
	if err != nil && b.engine == "webkit" {
		if strings.Contains(err.Error(), "intercepts pointer events") && b.webkitElementUnoccludedAtCenter(ctx, targetLocator, timeout) {
			return targetLocator.Tap(playwright.LocatorTapOptions{
				Force:   playwright.Bool(true),
				Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
			})
		}
		if timeout > tapTimeout && ctx.Err() == nil {
			err = targetLocator.Tap(playwright.LocatorTapOptions{
				Timeout: playwright.Float(b.timeoutMS(ctx, timeout-tapTimeout)),
			})
			if err != nil && strings.Contains(err.Error(), "intercepts pointer events") && b.webkitElementUnoccludedAtCenter(ctx, targetLocator, timeout) {
				return targetLocator.Tap(playwright.LocatorTapOptions{
					Force:   playwright.Bool(true),
					Timeout: playwright.Float(b.timeoutMS(ctx, timeout)),
				})
			}
		}
	}
	return err
}

func (b *Browser) webkitElementUnoccludedAtCenter(ctx context.Context, locator playwright.Locator, timeout time.Duration) bool {
	if locator == nil {
		return false
	}
	val, err := locator.Evaluate(`(el) => {
		if (!el) return false;
		el.scrollIntoView({ block: 'center', inline: 'center' });
		const r = el.getBoundingClientRect();
		if (r.width <= 0 || r.height <= 0) return false;
		const hit = document.elementFromPoint(r.left + r.width / 2, r.top + r.height / 2);
		return Boolean(hit && (hit === el || el.contains(hit) || hit.contains(el)));
	}`, nil, playwright.LocatorEvaluateOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, min(timeout, 2*time.Second))),
	})
	if err != nil {
		return false
	}
	ok, _ := val.(bool)
	return ok
}

func (b *Browser) shouldUseSyntheticTap() bool {
	return !b.touchMode
}

func syntheticTapOperation() string {
	return `
		dispatchPointer('pointerdown', x, y);
		dispatchTouch('touchstart', x, y, true);
		dispatchTouch('touchend', x, y, false);
		dispatchPointer('pointerup', x, y);
		dispatchClick(x, y);
	`
}

func (b *Browser) dispatchLongPress(ctx context.Context, action *protocol.Action, selector string, duration time.Duration) error {
	if err := b.runTouchScript(ctx, action, selector, `
		dispatchPointer('pointerdown', x, y);
		dispatchTouch('touchstart', x, y, true);
	`, "browser long press start"); err != nil {
		return err
	}
	if err := b.sleep(ctx, duration); err != nil {
		return err
	}
	return b.runTouchScript(ctx, action, selector, `
		dispatchTouch('touchend', x, y, false);
		dispatchPointer('pointerup', x, y);
		dispatchClick(x, y);
	`, "browser long press end")
}

func (b *Browser) dispatchSwipe(ctx context.Context, action *protocol.Action, selector, direction string, distance int) error {
	return b.runTouchScript(ctx, action, selector, fmt.Sprintf(`
		const startX = x;
		const startY = y;
		let endX = startX;
		let endY = startY;
		switch (%q) {
		case 'right':
			endX = startX + %d;
			break;
		case 'up':
			endY = startY - %d;
			break;
		case 'down':
			endY = startY + %d;
			break;
		default:
			endX = startX - %d;
			break;
		}
		dispatchPointer('pointerdown', startX, startY);
		dispatchTouch('touchstart', startX, startY, true);
		const steps = 5;
		for (let i = 1; i <= steps; i++) {
			const nextX = startX + ((endX - startX) * i / steps);
			const nextY = startY + ((endY - startY) * i / steps);
			dispatchPointer('pointermove', nextX, nextY);
			dispatchTouch('touchmove', nextX, nextY, true);
		}
		dispatchTouch('touchend', endX, endY, false);
		dispatchPointer('pointerup', endX, endY);
	`, normalizeSwipeDirection(direction), distance, distance, distance, distance), "browser swipe")
}

func normalizeSwipeDirection(direction string) string {
	switch strings.ToLower(strings.TrimSpace(direction)) {
	case "right":
		return "right"
	case "up":
		return "up"
	case "down":
		return "down"
	default:
		return "left"
	}
}

func (b *Browser) runTouchScript(ctx context.Context, action *protocol.Action, selector, operation, label string) error {
	if err := b.waitVisibleForAction(ctx, action, selector); err != nil {
		return err
	}
	locator, ok := b.actionLocator(action, selector)
	if !ok {
		page := b.currentPage()
		if page == nil {
			return fmt.Errorf("%s failed: no active page", label)
		}
		locator = firstMatch(page, selector)
	}

	expression := fmt.Sprintf(`(el) => {
		if (!el) {
			return 'selector not found';
		}
		el.scrollIntoView({ block: 'center', inline: 'center' });
		const rect = el.getBoundingClientRect();
		const x = rect.left + (rect.width / 2);
		const y = rect.top + (rect.height / 2);
		const makeTouchList = (clientX, clientY, active) => {
			const touchLike = {
				identifier: Date.now(),
				target: el,
				clientX,
				clientY,
				pageX: clientX + window.scrollX,
				pageY: clientY + window.scrollY,
				screenX: clientX,
				screenY: clientY,
				radiusX: 2,
				radiusY: 2,
				rotationAngle: 0,
				force: 1,
			};
			if (typeof Touch === 'function') {
				try {
					const touch = new Touch(touchLike);
					return { changed: [touch], active: active ? [touch] : [] };
				} catch (_) {
					// WebKit exposes Touch in some contexts where it is not constructible.
				}
			}
			return { changed: [touchLike], active: active ? [touchLike] : [] };
		};
		const dispatchTouch = (type, clientX, clientY, active) => {
			const list = makeTouchList(clientX, clientY, active);
			let event;
			if (typeof TouchEvent === 'function') {
				try {
					event = new TouchEvent(type, {
						bubbles: true,
						cancelable: true,
						composed: true,
						touches: list.active,
						targetTouches: list.active,
						changedTouches: list.changed,
					});
				} catch (_) {
					// Fall through to an Event carrying touch-list properties.
				}
			}
			if (!event) {
				event = new Event(type, { bubbles: true, cancelable: true, composed: true });
				Object.defineProperty(event, 'touches', { value: list.active });
				Object.defineProperty(event, 'targetTouches', { value: list.active });
				Object.defineProperty(event, 'changedTouches', { value: list.changed });
			}
			el.dispatchEvent(event);
		};
		const dispatchPointer = (type, clientX, clientY) => {
			if (typeof PointerEvent !== 'function') {
				return;
			}
			el.dispatchEvent(new PointerEvent(type, {
				bubbles: true,
				cancelable: true,
				composed: true,
				pointerType: 'touch',
				isPrimary: true,
				clientX,
				clientY,
			}));
		};
		const dispatchClick = (clientX, clientY) => {
			el.dispatchEvent(new MouseEvent('click', {
			bubbles: true,
			cancelable: true,
			composed: true,
			clientX,
			clientY,
		}));
		};
		%s
		return '';
	}`, operation)
	value, err := locator.Evaluate(expression, nil, playwright.LocatorEvaluateOptions{
		Timeout: playwright.Float(b.timeoutMS(ctx, b.actionTimeout(action))),
	})
	if err != nil {
		return err
	}
	evalErr, _ := value.(string)
	if evalErr != "" {
		return fmt.Errorf("%s failed: %s", label, evalErr)
	}
	return nil
}
