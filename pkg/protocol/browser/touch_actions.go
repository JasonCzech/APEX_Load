package browser

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/jasonczech/apexload/pkg/protocol"
	playwright "github.com/playwright-community/playwright-go"
)

func (b *Browser) dispatchTap(ctx context.Context, action *protocol.Action, selector string) error {
	return b.runTouchScript(ctx, action, selector, `
		dispatchPointer('pointerdown', x, y);
		dispatchTouch('touchstart', x, y, true);
		dispatchTouch('touchend', x, y, false);
		dispatchPointer('pointerup', x, y);
		dispatchClick(x, y);
	`, "browser tap")
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
		locator = page.Locator(selector)
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
				const touch = new Touch(touchLike);
				return { changed: [touch], active: active ? [touch] : [] };
			}
			return { changed: [touchLike], active: active ? [touchLike] : [] };
		};
		const dispatchTouch = (type, clientX, clientY, active) => {
			const list = makeTouchList(clientX, clientY, active);
			let event;
			if (typeof TouchEvent === 'function') {
				event = new TouchEvent(type, {
					bubbles: true,
					cancelable: true,
					composed: true,
					touches: list.active,
					targetTouches: list.active,
					changedTouches: list.changed,
				});
			} else {
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
