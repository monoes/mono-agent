// limiter(n) runs at most n async tasks at once; the rest wait their turn.
// Used where one UI element = one CLI process (e.g. image thumbnails).
export function limiter(n) {
  let active = 0
  const queue = []
  const next = () => {
    if (active >= n || queue.length === 0) return
    active++
    const { task, resolve, reject } = queue.shift()
    Promise.resolve().then(task).then(resolve, reject).finally(() => { active--; next() })
  }
  return (task) => new Promise((resolve, reject) => { queue.push({ task, resolve, reject }); next() })
}
