/** A deliberate downward gesture enters once; upward intent always returns control. */
export class EntranceGesture {
  private distance = 0;
  private lastTime = 0;
  private blocked = false;
  rearm() {
    if (this.blocked) this.reset();
  }
  reset() {
    this.distance = 0;
    this.blocked = false;
  }
  cancel() {
    this.distance = 0;
    this.blocked = true;
  }
  move(x: number, y: number, time: number, eligible: boolean, animating: boolean) {
    if (Math.abs(x) >= Math.abs(y)) return 'pass';
    if (y < 0) {
      this.distance = 0;
      if (animating) this.cancel();
      return animating ? 'cancel' : 'pass';
    }
    if (animating) return 'hold';
    if (!eligible || this.blocked) return 'pass';
    if (time - this.lastTime > 180) this.distance = 0;
    this.lastTime = time;
    this.distance += y;
    if (this.distance < 24) return 'pass';
    this.cancel();
    return 'enter';
  }
}
