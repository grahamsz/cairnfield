package app.cairnfield.mobile

import android.animation.Animator
import android.animation.ValueAnimator
import android.content.Context
import android.graphics.Canvas
import android.graphics.Color
import android.graphics.Matrix
import android.graphics.Paint
import android.graphics.Path
import android.graphics.RectF
import android.util.AttributeSet
import android.view.View
import android.view.animation.AccelerateDecelerateInterpolator
import android.view.animation.Interpolator
import android.view.animation.LinearInterpolator
import android.view.animation.OvershootInterpolator
import androidx.core.graphics.PathParser
import kotlin.math.min
import kotlin.math.sin

/**
 * Draws the cairnfield cairn: the three stones drop in from above one at a
 * time (bottom, middle, top), the stacked cairn rocks side to side a couple
 * of times, then the settled logo holds with a subtle pulse until the page is
 * ready. Stone geometry comes straight from logo.svg (viewBox 0 0 52 52 with
 * the group offset baked in).
 */
class CairnfieldLoadingView @JvmOverloads constructor(
    context: Context,
    attrs: AttributeSet? = null,
    defStyleAttr: Int = 0
) : View(context, attrs, defStyleAttr) {
    private val navyPaint = fillPaint(NAVY)
    private val orangePaint = fillPaint(ORANGE)
    private val bottomPath = stonePath(STONE_BOTTOM)
    private val middlePath = stonePath(STONE_MIDDLE)
    private val topPath = stonePath(STONE_TOP)
    private val dropEase: Interpolator = OvershootInterpolator(DROP_OVERSHOOT)
    private val linear: Interpolator = LinearInterpolator()

    private val rockPivotX: Float
    private val rockPivotY: Float
    private val pulsePivotX: Float
    private val pulsePivotY: Float

    private var animator: ValueAnimator? = null
    private var pulseAnimator: ValueAnimator? = null
    private var timelineProgress = 0f
    private var pulseProgress = 0f
    private var animationCancelled = false
    private var completionDelivered = false

    var onAnimationComplete: (() -> Unit)? = null
        set(value) {
            field = value
            if (completionDelivered) value?.invoke()
        }

    val animationsEnabled: Boolean
        get() = ValueAnimator.areAnimatorsEnabled()

    val elapsedMs: Long
        get() = (timelineProgress * DURATION_MS).toLong().coerceIn(0L, DURATION_MS)

    val isAnimationComplete: Boolean
        get() = completionDelivered

    init {
        val bottomBounds = stoneBounds(bottomPath)
        val topBounds = stoneBounds(topPath)
        rockPivotX = bottomBounds.centerX()
        rockPivotY = bottomBounds.bottom
        pulsePivotX = bottomBounds.centerX()
        pulsePivotY = (topBounds.top + bottomBounds.bottom) / 2f
        setBackgroundColor(BACKGROUND)
        importantForAccessibility = IMPORTANT_FOR_ACCESSIBILITY_NO
    }

    @Suppress("DEPRECATION") // The single-argument computeBounds requires API 34.
    private fun stoneBounds(path: Path): RectF {
        val bounds = RectF()
        path.computeBounds(bounds, true)
        return bounds
    }

    fun start(startElapsedMs: Long = 0L) {
        animationCancelled = true
        cancelAnimators()
        animationCancelled = false
        completionDelivered = false
        pulseProgress = 0f

        val clampedElapsed = startElapsedMs.coerceIn(0L, DURATION_MS)
        timelineProgress = clampedElapsed.toFloat() / DURATION_MS
        invalidate()

        if (!animationsEnabled || clampedElapsed == DURATION_MS) {
            timelineProgress = 1f
            invalidate()
            deliverCompletion()
            return
        }

        val remainingMs = DURATION_MS - clampedElapsed
        val nextAnimator = ValueAnimator.ofFloat(timelineProgress, 1f)
        nextAnimator.duration = remainingMs
        nextAnimator.interpolator = LinearInterpolator()
        nextAnimator.addUpdateListener { valueAnimator ->
            timelineProgress = valueAnimator.animatedValue as Float
            invalidate()
        }
        nextAnimator.addListener(object : Animator.AnimatorListener {
            override fun onAnimationStart(animation: Animator) = Unit

            override fun onAnimationEnd(animation: Animator) {
                if (animationCancelled) return
                timelineProgress = 1f
                invalidate()
                deliverCompletion()
                startPulse()
            }

            override fun onAnimationCancel(animation: Animator) = Unit

            override fun onAnimationRepeat(animation: Animator) = Unit
        })
        animator = nextAnimator
        nextAnimator.start()
    }

    fun cancel() {
        animationCancelled = true
        cancelAnimators()
    }

    override fun onDetachedFromWindow() {
        cancel()
        super.onDetachedFromWindow()
    }

    override fun onDraw(canvas: Canvas) {
        super.onDraw(canvas)
        if (width == 0 || height == 0) return

        val scale = min(width / VIEW_SIZE, height / VIEW_SIZE)
        val translateX = (width - VIEW_SIZE * scale) / 2f
        val translateY = (height - VIEW_SIZE * scale) / 2f
        val offscreenDistance = height * OFFSCREEN_HEIGHT_FRACTION / scale

        canvas.save()
        canvas.translate(translateX, translateY)
        canvas.scale(scale, scale)
        canvas.translate(LOGO_OFFSET_X, LOGO_OFFSET_Y)

        val pulse = 1f + PULSE_AMPLITUDE * pulseProgress
        if (pulse != 1f) canvas.scale(pulse, pulse, pulsePivotX, pulsePivotY)
        val rock = rockAngle()
        if (rock != 0f) canvas.rotate(rock, rockPivotX, rockPivotY)

        drawStone(canvas, bottomPath, navyPaint, dropOffset(BOTTOM_DROP_START, BOTTOM_DROP_END, offscreenDistance))
        drawStone(canvas, middlePath, navyPaint, dropOffset(MIDDLE_DROP_START, MIDDLE_DROP_END, offscreenDistance))
        drawStone(canvas, topPath, orangePaint, dropOffset(TOP_DROP_START, TOP_DROP_END, offscreenDistance))
        canvas.restore()
    }

    private fun drawStone(canvas: Canvas, path: Path, paint: Paint, offsetY: Float) {
        if (offsetY == 0f) {
            canvas.drawPath(path, paint)
            return
        }
        canvas.save()
        canvas.translate(0f, offsetY)
        canvas.drawPath(path, paint)
        canvas.restore()
    }

    private fun dropOffset(start: Float, end: Float, offscreenDistance: Float): Float {
        val progress = phase(timelineProgress, start, end, dropEase)
        return -offscreenDistance * (1f - progress)
    }

    private fun rockAngle(): Float {
        val progress = phase(timelineProgress, ROCK_START, ROCK_END, linear)
        if (progress <= 0f || progress >= 1f) return 0f
        val angle = ROCK_AMPLITUDE_DEG * sin(progress * ROCK_CYCLES * 2f * Math.PI).toFloat()
        return angle * (1f - progress)
    }

    private fun startPulse() {
        if (animationCancelled || !animationsEnabled) return
        val nextAnimator = ValueAnimator.ofFloat(0f, 1f)
        nextAnimator.duration = PULSE_DURATION_MS
        nextAnimator.repeatCount = ValueAnimator.INFINITE
        nextAnimator.repeatMode = ValueAnimator.REVERSE
        nextAnimator.interpolator = AccelerateDecelerateInterpolator()
        nextAnimator.addUpdateListener { valueAnimator ->
            pulseProgress = valueAnimator.animatedValue as Float
            invalidate()
        }
        pulseAnimator = nextAnimator
        nextAnimator.start()
    }

    private fun deliverCompletion() {
        if (completionDelivered || animationCancelled) return
        completionDelivered = true
        onAnimationComplete?.invoke()
    }

    private fun cancelAnimators() {
        animator?.cancel()
        animator = null
        pulseAnimator?.cancel()
        pulseAnimator = null
    }

    private fun phase(progress: Float, start: Float, end: Float, easing: Interpolator): Float {
        if (progress <= start) return 0f
        if (progress >= end) return 1f
        return easing.getInterpolation((progress - start) / (end - start))
    }

    private fun fillPaint(colorValue: Int) = Paint(Paint.ANTI_ALIAS_FLAG).apply {
        color = colorValue
        style = Paint.Style.FILL
    }

    companion object {
        const val DURATION_MS = 1_800L

        // Virtual canvas: the 52x52 logo occupies ~37% of the view's smaller side.
        private const val VIEW_SIZE = 140f
        private const val LOGO_OFFSET_X = 44f
        private const val LOGO_OFFSET_Y = 43.5f
        private const val OFFSCREEN_HEIGHT_FRACTION = 0.70f

        private const val BOTTOM_DROP_START = 0.00f
        private const val BOTTOM_DROP_END = 0.16f
        private const val MIDDLE_DROP_START = 0.17f
        private const val MIDDLE_DROP_END = 0.33f
        private const val TOP_DROP_START = 0.34f
        private const val TOP_DROP_END = 0.50f
        private const val ROCK_START = 0.56f
        private const val ROCK_END = 0.92f
        private const val ROCK_AMPLITUDE_DEG = 4.5f
        private const val ROCK_CYCLES = 2f
        private const val DROP_OVERSHOOT = 1.6f

        private const val PULSE_AMPLITUDE = 0.018f
        private const val PULSE_DURATION_MS = 2_600L

        private val BACKGROUND = Color.rgb(242, 240, 235)
        private val NAVY = Color.rgb(43, 52, 72)
        private val ORANGE = Color.rgb(196, 107, 68)

        // logo.svg group offset, baked into each stone path.
        private const val GROUP_DX = -41.385513f
        private const val GROUP_DY = -70.875101f

        private const val STONE_BOTTOM =
            "m 48.367239,123.3629 39.503364,0 a 2.8628085,2.8628085 127.69906 0 0 2.770343,-3.58453 l -2.019792,-7.75305 a 5.4243068,5.4243068 41.069622 0 0 -4.612383,-4.01934 l -26.519016,-3.13456 a 5.1984154,5.1984154 151.26908 0 0 -5.290556,2.9002 l -5.92413,12.25627 a 2.3237523,2.3237523 57.898525 0 0 2.09217,3.33501 z"
        private const val STONE_MIDDLE =
            "m 47.586122,94.818693 -0.643287,2.825946 a 3.4029464,3.4029464 55.397155 0 0 2.846215,4.125391 l 18.541001,2.59594 a 16.180632,16.180632 175.07594 0 0 7.194868,-0.61986 l 3.215548,-1.03353 a 6.1992652,6.1992652 131.3226 0 0 4.199197,-4.776049 l 1.151839,-6.236897 a 2.8210388,2.8210388 47.755926 0 0 -3.017628,-3.32284 l -28.975241,2.510399 a 5.0774254,5.0774254 138.93618 0 0 -4.512512,3.9315 z"
        private const val STONE_TOP =
            "m 55.466971,85.146788 0.951198,-7.518431 a 7.4994774,7.4994774 123.49633 0 1 3.665766,-5.539139 8.2835039,8.2835039 174.8089 0 1 5.346876,-0.485766 l 6.009954,0.80199 a 6.1347681,6.1347681 38.724386 0 1 4.947754,3.967357 l 1.945245,5.300698 a 3.0173964,3.0173964 120.68183 0 1 -2.387496,4.023908 l -17.280595,2.577751 a 2.8067907,2.8067907 44.363113 0 1 -3.198702,-3.128368 z"

        private fun stonePath(pathData: String): Path {
            val path = PathParser.createPathFromPathData(pathData)
            path.transform(Matrix().apply { postTranslate(GROUP_DX, GROUP_DY) })
            return path
        }
    }
}
