/** Shared ticking clock so relative times ("vor 3 Min.") stay fresh. */
class Clock {
	now = $state(Date.now());
	#timer: ReturnType<typeof setInterval> | undefined;

	start(): () => void {
		this.now = Date.now();
		this.#timer ??= setInterval(() => (this.now = Date.now()), 30_000);
		return () => {
			clearInterval(this.#timer);
			this.#timer = undefined;
		};
	}
}

export const clock = new Clock();
