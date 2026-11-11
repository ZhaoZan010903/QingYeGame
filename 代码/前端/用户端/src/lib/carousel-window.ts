export function carouselWindow(total: number, active: number) {
  const length = Math.min(5, total);
  const start = Math.max(0, Math.min(active - 2, total - length));
  return Array.from({ length }, (_, index) => start + index);
}
export function preloadCarouselSlide(index: number, active: number, total: number) {
  const distance = Math.abs(index - active);
  return Math.min(distance, total - distance) <= 1;
}
